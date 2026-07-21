// Package session implements a domain-agnostic interactive simulation
// controller. It owns session lifecycle (start/status/end/reset) and emits
// schedule-driven beats on a session-scoped stream.
//
// # Session epochs (C3)
//
// A simulation session is a durable epoch identified by SessionID. Optional
// Config.Active (*ActiveSession) is the in-process pointer composition shares
// with API read ports: Start/Reset set it, explicit End clears it (empty board).
// Natural beat completion leaves Active set so the final COP remains visible.
//
// This package lives under internal/simulation. Framework packages
// (ingestion, store, terra, sol, luna, ontology, contracts, stream, api
// production code, projectors, …) must never import it — simulation imports
// framework packages and orchestrates over time. All pacing/timing lives under
// the simulation tree; the framework has no notion of beat delay beyond the
// schedule types already defined in contracts.
//
// # Pacing ownership
//
// BeatExecutor (package simulation) is the owner of Append-path pacing for the
// real EventLog path (equal spacing / burst). This session controller owns only
// SSE beat emission timing for the interactive lifecycle stream.
//
// Delay model (SSE presentation):
//
//   - Default (BeatSpacing == 0): each ScheduledBeat.Delay is relative to
//     session start (historical C1 behaviour). A beat with Delay=2s fires two
//     seconds after start regardless of earlier beats. Tests rely on this.
//   - Equal spacing (BeatSpacing > 0): beat at sorted index i fires after
//     i*BeatSpacing from session start; ScheduledBeat.Delay is ignored.
//     Composition should set BeatSpacing from MOSAIC_SIM_BEAT_SPACING
//     (default 2.5s) so the demo is not flooded by fixture delay_ms≈100.
//
// Emission order always follows the Order field (ascending), not the schedule
// slice order.
//
// Stream backpressure: each subscriber has a bounded buffer (default 64). When
// the buffer is full the oldest pending event is dropped so a slow consumer
// cannot block the controller indefinitely (drop-oldest).
//
// The controller holds no durable immutable event store. Reset creates a new
// SessionID and a fresh stream sequence; it never rewrites or truncates prior
// session history because none is retained here.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"mosaic.local/mosaic/internal/contracts"
)

const defaultSubscriberBuffer = 64

// Compile-time seam checks: concrete types satisfy contracts/adapters without
// those packages importing session. Keep sessionControllerSurface aligned with
// api.SimulationController (composition wires *Controller there).
var (
	_ contracts.SimulationStreamSubscription = (*Subscription)(nil)
	_ sessionControllerSurface               = (*Controller)(nil)
)

// sessionControllerSurface is the method set HTTP adapters and composition
// require. Defined here (not in contracts) so contracts stay free of a
// controller interface while still catching signature drift at compile time.
type sessionControllerSurface interface {
	Start(ctx context.Context) (contracts.SimulationSession, error)
	Status() contracts.SimulationSession
	End(ctx context.Context) (contracts.SimulationSession, error)
	Reset(ctx context.Context) (contracts.SimulationSession, error)
	Subscribe() contracts.SimulationStreamSubscription
}

var (
	// ErrNilSchedule means Config.Schedule was not provided.
	ErrNilSchedule = errors.New("simulation schedule is required")
	// ErrAlreadyRunning means Start was called while a session is running.
	// Use Reset to end the current session and begin a new one. Alias of the
	// shared contracts sentinel so HTTP adapters can map it without importing
	// this package.
	ErrAlreadyRunning = contracts.ErrSimulationAlreadyRunning
)

// Config wires a controller. Schedule is required; clock and timers are
// injectable so tests can prove deterministic timing without real sleeps.
type Config struct {
	Schedule contracts.SimulationSchedule

	// Clock returns the current time for event timestamps and delay math.
	// Defaults to time.Now.
	Clock func() time.Time

	// After returns a channel that receives once after duration d (like
	// time.After). Defaults to time.After. Inject VirtualClock.After for
	// deterministic, advanceable timing in tests.
	After func(d time.Duration) <-chan time.Time

	// NewSessionID creates a unique session identifier. Defaults to a random
	// hex id with a "sim-" prefix.
	NewSessionID func() string

	// SubscriberBuffer is the per-subscriber channel capacity. Zero selects
	// the package default. Values less than 1 are treated as 1.
	SubscriberBuffer int

	// BeatSpacing enables equal-spacing SSE pacing when > 0: the beat at
	// sorted index i fires after i*BeatSpacing from session start, ignoring
	// each ScheduledBeat.Delay. Zero (default) keeps relative-to-start Delay
	// so existing tests and callers remain unchanged. Composition should set
	// this from simulation.BeatSpacingFromEnv() for the interactive demo.
	BeatSpacing time.Duration

	// Active is the optional in-process session epoch holder shared with API
	// read ports (C3). When set, Start/Reset call Active.Set(sessionID) and
	// End calls Active.Clear so "no active session" yields an empty board.
	// Natural beat completion leaves Active set so the final COP remains visible.
	Active *ActiveSession

	// OnBeat is invoked after each successful SSE beat emission (mutex released).
	// Composition uses it for the progressive EventLog path: Append the raw
	// event, then synchronously ingest+project (+ advisory continuum). A non-nil
	// error stops further beats and ends the session cleanly (Active left set so
	// partial progress remains visible). Nil means SSE-only emission.
	OnBeat func(ctx context.Context, beat contracts.ScheduledBeat) error
}

// Controller is a session-scoped simulation lifecycle engine. Start/End/Reset
// are serialized; Status is safe for concurrent readers.
type Controller struct {
	schedule         contracts.SimulationSchedule
	clock            func() time.Time
	after            func(d time.Duration) <-chan time.Time
	newSessionID     func() string
	subscriberBuffer int
	beatSpacing      time.Duration
	active           *ActiveSession
	onBeat           func(ctx context.Context, beat contracts.ScheduledBeat) error

	mu        sync.RWMutex
	session   contracts.SimulationSession
	sequence  int64
	runCancel context.CancelFunc
	runDone   chan struct{}

	nextSubID uint64
	subs      map[uint64]chan contracts.SimulationStreamEvent
}

// Subscription is a caller-owned registration on the session-scoped stream.
// Call Cancel when finished; it is safe to call more than once.
// It implements contracts.SimulationStreamSubscription.
type Subscription struct {
	events <-chan contracts.SimulationStreamEvent

	once   sync.Once
	cancel func()
}

// Events returns the receive-only channel of session-scoped stream events.
func (s *Subscription) Events() <-chan contracts.SimulationStreamEvent {
	if s == nil || s.events == nil {
		return closedEvents()
	}
	return s.events
}

// New constructs a controller in pending status with a copy of the schedule
// beats. No session id is assigned until Start or Reset.
func New(config Config) (*Controller, error) {
	if config.Schedule == nil {
		return nil, ErrNilSchedule
	}
	clock := config.Clock
	if clock == nil {
		clock = time.Now
	}
	after := config.After
	if after == nil {
		after = time.After
	}
	newID := config.NewSessionID
	if newID == nil {
		newID = randomSessionID
	}
	buf := config.SubscriberBuffer
	if buf == 0 {
		buf = defaultSubscriberBuffer
	}
	if buf < 1 {
		buf = 1
	}

	beats := copyBeats(config.Schedule.Beats())
	return &Controller{
		schedule:         config.Schedule,
		clock:            clock,
		after:            after,
		newSessionID:     newID,
		subscriberBuffer: buf,
		beatSpacing:      config.BeatSpacing,
		active:           config.Active,
		onBeat:           config.OnBeat,
		session: contracts.SimulationSession{
			Status: contracts.SessionPending,
			Beats:  beats,
		},
		subs: make(map[uint64]chan contracts.SimulationStreamEvent),
	}, nil
}

// Start creates a new synthetic session, transitions pending→running, emits
// workspace_clear then a status_change, and begins schedule-driven beat
// emission. It fails if a session is already running (use Reset).
func (c *Controller) Start(ctx context.Context) (contracts.SimulationSession, error) {
	if c == nil {
		return contracts.SimulationSession{}, errors.New("nil controller")
	}
	if err := ctx.Err(); err != nil {
		return contracts.SimulationSession{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.session.Status == contracts.SessionRunning {
		return c.snapshotLocked(), ErrAlreadyRunning
	}
	return c.startLocked()
}

// Status returns a copy of the current session (id, status, beats).
func (c *Controller) Status() contracts.SimulationSession {
	if c == nil {
		return contracts.SimulationSession{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshotLocked()
}

// End cancels in-flight beat emission and transitions the active session to
// ended, emitting status_change. It is idempotent when not running.
func (c *Controller) End(ctx context.Context) (contracts.SimulationSession, error) {
	if c == nil {
		return contracts.SimulationSession{}, errors.New("nil controller")
	}
	_ = ctx // reserved for future deadline propagation; cancel is internal

	cancel, done := c.beginStop()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session.Status == contracts.SessionRunning {
		c.session.Status = contracts.SessionEnded
		c.publishLocked(contracts.StreamEventStatusChange, map[string]any{
			"status": string(contracts.SessionEnded),
		})
	}
	// Explicit End clears the active epoch so API read ports return an empty board.
	if c.active != nil {
		c.active.Clear()
	}
	return c.snapshotLocked(), nil
}

// Reset ends the current session if needed and starts a new one with a fresh
// SessionID and stream sequence. Prior session stream signals are not
// re-emitted; the controller does not retain durable immutable history.
func (c *Controller) Reset(ctx context.Context) (contracts.SimulationSession, error) {
	if c == nil {
		return contracts.SimulationSession{}, errors.New("nil controller")
	}
	if err := ctx.Err(); err != nil {
		return contracts.SimulationSession{}, err
	}

	cancel, done := c.beginStop()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.session.Status == contracts.SessionRunning {
		c.session.Status = contracts.SessionEnded
		c.publishLocked(contracts.StreamEventStatusChange, map[string]any{
			"status": string(contracts.SessionEnded),
		})
	}
	return c.startLocked()
}

// Subscribe registers a bounded local subscriber. Events carry the active
// session's SessionID. The caller must Cancel the subscription.
// The return type is the contracts seam so composition can wire *Controller into
// framework adapters without those adapters importing this package.
func (c *Controller) Subscribe() contracts.SimulationStreamSubscription {
	if c == nil {
		return &Subscription{events: closedEvents()}
	}

	c.mu.Lock()
	id := c.nextSubID
	c.nextSubID++
	ch := make(chan contracts.SimulationStreamEvent, c.subscriberBuffer)
	c.subs[id] = ch
	c.mu.Unlock()

	return &Subscription{
		events: ch,
		cancel: func() {
			c.mu.Lock()
			if existing, ok := c.subs[id]; ok {
				delete(c.subs, id)
				close(existing)
			}
			c.mu.Unlock()
		},
	}
}

// Cancel removes the subscriber. Safe to call more than once.
func (s *Subscription) Cancel() {
	if s == nil || s.cancel == nil {
		return
	}
	s.once.Do(s.cancel)
}

// startLocked assumes c.mu is held and the controller is not running.
func (c *Controller) startLocked() (contracts.SimulationSession, error) {
	// Ensure any prior runner slot is clear (status should not be running).
	c.runCancel = nil
	c.runDone = nil

	beats := copyBeats(c.schedule.Beats())
	sessionID := c.newSessionID()
	if sessionID == "" {
		return c.snapshotLocked(), errors.New("session id generator returned empty id")
	}

	c.session = contracts.SimulationSession{
		SessionID: sessionID,
		Status:    contracts.SessionRunning,
		Beats:     beats,
	}
	c.sequence = 0

	// Publish the new epoch before beat emission so concurrent COP/advisory
	// reads resolve the correct materialization key immediately.
	if c.active != nil {
		c.active.Set(sessionID)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.runCancel = cancel
	c.runDone = done
	startTime := c.clock().UTC()

	c.publishLocked(contracts.StreamEventWorkspaceClear, nil)
	c.publishLocked(contracts.StreamEventStatusChange, map[string]any{
		"status": string(contracts.SessionRunning),
	})

	go c.runBeats(runCtx, done, sessionID, beats, startTime)

	return c.snapshotLocked(), nil
}

// beginStop captures cancel/done under lock so End/Reset can wait without
// holding the mutex (avoids deadlock with the runner).
func (c *Controller) beginStop() (context.CancelFunc, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cancel := c.runCancel
	done := c.runDone
	c.runCancel = nil
	// leave runDone for the waiter; runner closes it
	return cancel, done
}

func (c *Controller) runBeats(
	ctx context.Context,
	done chan struct{},
	sessionID string,
	beats []contracts.ScheduledBeat,
	startTime time.Time,
) {
	defer close(done)

	ordered := copyBeats(beats)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Order < ordered[j].Order
	})

	completed := true
	for i, beat := range ordered {
		delay := c.delayFor(i, beat)
		if err := c.waitUntil(ctx, startTime, delay); err != nil {
			completed = false
			break
		}
		if !c.emitBeat(sessionID, beat) {
			completed = false
			break
		}
		// OnBeat runs after SSE emission with the controller mutex released so
		// domain work (EventLog.Append + sync ingest) cannot deadlock Status/End.
		if c.onBeat != nil {
			if err := c.onBeat(ctx, beat); err != nil {
				completed = false
				break
			}
		}
	}

	// Natural completion and OnBeat/cancel failure both end the session cleanly.
	// Active is left set (when present) so partial or final COP remains visible;
	// only explicit End clears the epoch. When End/Reset already transitioned
	// status, the SessionRunning guard is a no-op.
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session.SessionID != sessionID || c.session.Status != contracts.SessionRunning {
		return
	}
	c.session.Status = contracts.SessionEnded
	c.runCancel = nil
	c.publishLocked(contracts.StreamEventStatusChange, map[string]any{
		"status": string(contracts.SessionEnded),
	})
	_ = completed // retained for readability of the loop above
}

// delayFor returns the wait from session start for the beat at sorted index i.
// When BeatSpacing > 0, equal spacing is used (i * BeatSpacing). Otherwise the
// beat's own Delay (relative to start) is used. Formula matches
// simulation.EqualSpacingDelay without importing the parent package (avoids
// pulling executor deps into session tests).
func (c *Controller) delayFor(index int, beat contracts.ScheduledBeat) time.Duration {
	if c.beatSpacing > 0 {
		if index <= 0 || c.beatSpacing <= 0 {
			return 0
		}
		return time.Duration(index) * c.beatSpacing
	}
	if beat.Delay < 0 {
		return 0
	}
	return beat.Delay
}

// waitUntil blocks until clock reaches startTime+delay or ctx is cancelled.
// Delay is relative to session start (either equal-spacing or schedule Delay).
//
// Waiting is driven by the injectable After. When After fires without the
// clock advancing (valid for some test fakes), the wait completes so a frozen
// Clock cannot spin forever. When the clock does advance (VirtualClock), the
// remaining delay is re-checked so partial advances keep waiting.
func (c *Controller) waitUntil(ctx context.Context, startTime time.Time, delay time.Duration) error {
	if delay < 0 {
		delay = 0
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		now := c.clock().UTC()
		remaining := delay - now.Sub(startTime)
		if remaining <= 0 {
			return nil
		}
		timer := c.after(remaining)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer:
			later := c.clock().UTC()
			if !later.After(now) {
				return nil
			}
		}
	}
}

// emitBeat publishes a beat event when the session is still the active one.
// Returns false if the session was replaced or cancelled.
func (c *Controller) emitBeat(sessionID string, beat contracts.ScheduledBeat) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session.SessionID != sessionID || c.session.Status != contracts.SessionRunning {
		return false
	}
	c.publishLocked(contracts.StreamEventBeat, map[string]any{
		"beat_id":      beat.BeatID,
		"order":        beat.Order,
		"raw_event_id": beat.RawEventID,
	})
	return true
}

func (c *Controller) publishLocked(eventType contracts.StreamEventType, payload any) {
	c.sequence++
	event := contracts.SimulationStreamEvent{
		SessionID: c.session.SessionID,
		Sequence:  c.sequence,
		Timestamp: c.clock().UTC(),
		Type:      eventType,
		Payload:   payload,
	}
	for _, ch := range c.subs {
		offerDropOldest(ch, event)
	}
}

func offerDropOldest(ch chan contracts.SimulationStreamEvent, event contracts.SimulationStreamEvent) {
	select {
	case ch <- event:
		return
	default:
	}
	// Drop oldest, then try once more without blocking the controller.
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- event:
	default:
	}
}

func (c *Controller) snapshotLocked() contracts.SimulationSession {
	return contracts.SimulationSession{
		SessionID: c.session.SessionID,
		Status:    c.session.Status,
		Beats:     copyBeats(c.session.Beats),
	}
}

func copyBeats(beats []contracts.ScheduledBeat) []contracts.ScheduledBeat {
	if len(beats) == 0 {
		return nil
	}
	out := make([]contracts.ScheduledBeat, len(beats))
	copy(out, beats)
	return out
}

func randomSessionID() string {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("sim-%d", time.Now().UTC().UnixNano())
	}
	return "sim-" + hex.EncodeToString(bytes)
}

// closedEventsChan is a permanently closed channel shared by nil-safe
// Subscription.Events and Subscribe on a nil controller.
var closedEventsChan = func() <-chan contracts.SimulationStreamEvent {
	ch := make(chan contracts.SimulationStreamEvent)
	close(ch)
	return ch
}()

func closedEvents() <-chan contracts.SimulationStreamEvent {
	return closedEventsChan
}
