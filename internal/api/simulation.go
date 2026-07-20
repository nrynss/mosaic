package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/simsession"
	"mosaic.local/mosaic/internal/stream"
)

// SimulationController is the optional interactive session lifecycle surface
// consumed by the HTTP adapter. Composition wires *simsession.Controller
// (which satisfies this interface); tests may inject a real controller or stub.
type SimulationController interface {
	Start(ctx context.Context) (contracts.SimulationSession, error)
	Status() contracts.SimulationSession
	End(ctx context.Context) (contracts.SimulationSession, error)
	Reset(ctx context.Context) (contracts.SimulationSession, error)
	Subscribe() *simsession.Subscription
}

// handleSimulationStart creates a new synthetic session and begins beat emission.
// A session that is already running returns 409; clients should call reset.
func (s *Server) handleSimulationStart(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) || !s.authorize(w, r, ActionControlSimulation) {
		return
	}
	ctrl, ok := s.requireSimulation(w)
	if !ok {
		return
	}
	session, err := ctrl.Start(r.Context())
	if err != nil {
		if errors.Is(err, simsession.ErrAlreadyRunning) {
			writeJSON(w, http.StatusConflict, sessionPayload(session), apiError{
				Code:    "simulation_already_running",
				Message: "simulation session already running; use POST /api/v1/simulation/reset to start a new session",
			})
			return
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			writeJSON(w, http.StatusRequestTimeout, nil, apiError{
				Code:    "simulation_start_canceled",
				Message: "simulation start was canceled",
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, nil, apiError{
			Code:    "simulation_start_failed",
			Message: "could not start simulation session",
		})
		return
	}
	writeJSON(w, http.StatusOK, sessionPayload(session), nil)
}

// handleSimulationReset ends any active session and starts a fresh one with a
// new SessionID. Reset only affects the in-process controller session; it never
// truncates or rewrites the append-only immutable store.
func (s *Server) handleSimulationReset(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) || !s.authorize(w, r, ActionControlSimulation) {
		return
	}
	ctrl, ok := s.requireSimulation(w)
	if !ok {
		return
	}
	session, err := ctrl.Reset(r.Context())
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			writeJSON(w, http.StatusRequestTimeout, nil, apiError{
				Code:    "simulation_reset_canceled",
				Message: "simulation reset was canceled",
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, nil, apiError{
			Code:    "simulation_reset_failed",
			Message: "could not reset simulation session",
		})
		return
	}
	writeJSON(w, http.StatusOK, sessionPayload(session), nil)
}

// handleSimulationStatus returns the current controller session snapshot.
func (s *Server) handleSimulationStatus(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) || !s.authorize(w, r, ActionReadSimulation) {
		return
	}
	ctrl, ok := s.requireSimulation(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, sessionPayload(ctrl.Status()), nil)
}

// handleSimulationEnd explicitly ends the active session if one is running.
// It is idempotent when the session is not running.
func (s *Server) handleSimulationEnd(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) || !s.authorize(w, r, ActionControlSimulation) {
		return
	}
	ctrl, ok := s.requireSimulation(w)
	if !ok {
		return
	}
	session, err := ctrl.End(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, nil, apiError{
			Code:    "simulation_end_failed",
			Message: "could not end simulation session",
		})
		return
	}
	writeJSON(w, http.StatusOK, sessionPayload(session), nil)
}

// handleSimulationStream opens a session-scoped SSE subscription. Event names
// match StreamEventType values (workspace_clear, status_change, beat). Payloads
// never include raw event bodies — beat payloads carry raw_event_id only.
func (s *Server) handleSimulationStream(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) || !s.authorize(w, r, ActionReadSimulation) {
		return
	}
	ctrl, ok := s.requireSimulation(w)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, nil, apiError{
			Code:    "streaming_unsupported",
			Message: "response writer does not support streaming",
		})
		return
	}

	sub := ctrl.Subscribe()
	defer sub.Cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Immediate status snapshot so clients know the current session without
	// waiting for the next controller event.
	if err := writeSSE(w, stream.Event{
		Name: "session.snapshot",
		Data: sessionPayload(ctrl.Status()),
	}); err != nil {
		return
	}
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-sub.Events:
			if !open {
				return
			}
			if err := writeSSE(w, stream.Event{
				Name: string(event.Type),
				Data: streamEventPayload(event),
			}); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) requireSimulation(w http.ResponseWriter) (SimulationController, bool) {
	if s == nil || s.simulation == nil {
		writeJSON(w, http.StatusServiceUnavailable, nil, apiError{
			Code:    "simulation_unavailable",
			Message: "interactive simulation controller is not configured",
		})
		return nil, false
	}
	return s.simulation, true
}

// sessionPayload projects a SimulationSession for the HTTP wire. Beats expose
// identifiers and schedule metadata only — never raw source bodies.
func sessionPayload(session contracts.SimulationSession) map[string]any {
	beats := make([]map[string]any, 0, len(session.Beats))
	for _, beat := range session.Beats {
		beats = append(beats, map[string]any{
			"beat_id":      beat.BeatID,
			"order":        beat.Order,
			"raw_event_id": beat.RawEventID,
			// delay_ms is UI-friendly; Go's native Duration JSON is nanoseconds.
			"delay_ms": beat.Delay.Milliseconds(),
		})
	}
	return map[string]any{
		"session_id": session.SessionID,
		"status":     string(session.Status),
		"beats":      beats,
	}
}

// streamEventPayload serializes a session-scoped stream event. Beat payloads
// from the controller already contain only beat_id/order/raw_event_id.
func streamEventPayload(event contracts.SimulationStreamEvent) map[string]any {
	payload := map[string]any{
		"session_id": event.SessionID,
		"sequence":   event.Sequence,
		"timestamp":  event.Timestamp.UTC().Format(time.RFC3339Nano),
		"type":       string(event.Type),
	}
	if event.Payload != nil {
		payload["payload"] = event.Payload
	}
	return payload
}
