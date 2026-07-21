package simulation_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/eventlog"
	"mosaic.local/mosaic/internal/eventlog/memory"
	"mosaic.local/mosaic/internal/simulation"
	"mosaic.local/mosaic/internal/simulation/session"
)

// F1 progressive path proofs (simulation package).
//
// These tests lock the controller + EventLog composition pattern (not the real
// domain projector): OnBeat → EventLog.Append (raw.event,
// IdempotencyKey=raw_event_id) → per-beat process work that advances a local
// revision ladder. Real progressive materialization is covered by
// cmd/mosaicdemo and tests/e2e proofs.

// progressiveSchedule mirrors the composition schedule surface for unit proofs.
type progressiveSchedule struct {
	beats []contracts.ScheduledBeat
}

func (s progressiveSchedule) Beats() []contracts.ScheduledBeat {
	out := make([]contracts.ScheduledBeat, len(s.beats))
	copy(out, s.beats)
	return out
}

// TestF1ProgressiveOnBeatAppendsThenAdvancesRevisionLadder proves the
// controller + EventLog composition pattern: each beat Appends first, then
// mock process work advances a local revision ladder with intermediates —
// not a bulk jump. (Not domain ProcessBeat / projector.)
func TestF1ProgressiveOnBeatAppendsThenAdvancesRevisionLadder(t *testing.T) {
	log := memory.New()
	// 5 beats → final revision 5 via +1 per beat (unit ladder; fixture is 10→9).
	beats := []contracts.ScheduledBeat{
		{BeatID: "b1", Order: 1, RawEventID: "raw-1"},
		{BeatID: "b2", Order: 2, RawEventID: "raw-2"},
		{BeatID: "b3", Order: 3, RawEventID: "raw-3"},
		{BeatID: "b4", Order: 4, RawEventID: "raw-4"},
		{BeatID: "b5", Order: 5, RawEventID: "raw-5"},
	}
	payloads := map[string][]byte{
		"raw-1": []byte(`{"id":"1"}`),
		"raw-2": []byte(`{"id":"2"}`),
		"raw-3": []byte(`{"id":"3"}`),
		"raw-4": []byte(`{"id":"4"}`),
		"raw-5": []byte(`{"id":"5"}`),
	}

	var mu sync.Mutex
	var revisions []int64
	var revision int64
	var processOrder []string

	// Mirror cmd/mosaicdemo progressive OnBeat: Append then ProcessBeat-like work.
	onBeat := func(ctx context.Context, beat contracts.ScheduledBeat) error {
		payload, ok := payloads[beat.RawEventID]
		if !ok {
			return errors.New("missing payload for " + beat.RawEventID)
		}
		if err := log.Append(ctx, eventlog.EventEnvelope{
			PartitionKey:   "domestic-disturbance",
			IdempotencyKey: beat.RawEventID,
			Type:           simulation.EventTypeRawEvent,
			Payload:        payload,
		}); err != nil {
			return err
		}
		// "ProcessBeat": real work after Append advances the board.
		mu.Lock()
		revision++
		revisions = append(revisions, revision)
		processOrder = append(processOrder, beat.BeatID)
		// Intermediate visibility: appends so far equal process steps.
		if log.Len() != len(processOrder) {
			mu.Unlock()
			return errors.New("append count diverged from process steps (bulk path?)")
		}
		mu.Unlock()
		return nil
	}

	active := session.NewActiveSession()
	ctrl, err := session.New(session.Config{
		Schedule:    progressiveSchedule{beats: beats},
		BeatSpacing: time.Millisecond, // equal spacing; not flood of fixture delays
		Active:      active,
		OnBeat:      onBeat,
	})
	if err != nil {
		t.Fatalf("New controller: %v", err)
	}

	// Empty board before Play: no active session / no process work.
	if _, ok := active.ActiveSessionID(); ok {
		t.Fatal("active session set before Start")
	}
	mu.Lock()
	if revision != 0 {
		t.Fatalf("revision before Play = %d, want 0", revision)
	}
	mu.Unlock()
	if log.Len() != 0 {
		t.Fatalf("event log before Play = %d, want 0", log.Len())
	}

	if _, err := ctrl.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitCtrlEnded(t, ctrl, 5*time.Second)

	// Natural end leaves Active set (final board remains visible).
	if id, ok := active.ActiveSessionID(); !ok || id == "" {
		t.Fatal("active session cleared after natural end; want retained final board")
	}

	mu.Lock()
	defer mu.Unlock()
	if revision != 5 {
		t.Fatalf("final revision = %d, want 5 (per-beat work, not bulk seed)", revision)
	}
	if len(revisions) != 5 {
		t.Fatalf("intermediate revision samples = %d, want 5", len(revisions))
	}
	for i, rev := range revisions {
		if rev != int64(i+1) {
			t.Fatalf("revision ladder[%d] = %d, want %d (progressive)", i, rev, i+1)
		}
	}
	wantOrder := []string{"b1", "b2", "b3", "b4", "b5"}
	if len(processOrder) != len(wantOrder) {
		t.Fatalf("process order = %v, want %v", processOrder, wantOrder)
	}
	for i := range wantOrder {
		if processOrder[i] != wantOrder[i] {
			t.Fatalf("process order = %v, want %v", processOrder, wantOrder)
		}
	}

	// EventLog holds one envelope per beat with composition envelope shape.
	events := log.Events()
	if len(events) != 5 {
		t.Fatalf("event log len = %d, want 5", len(events))
	}
	for i, ev := range events {
		if ev.Type != simulation.EventTypeRawEvent {
			t.Errorf("event[%d] type = %q, want %q", i, ev.Type, simulation.EventTypeRawEvent)
		}
		if ev.IdempotencyKey != beats[i].RawEventID {
			t.Errorf("event[%d] key = %q, want %q", i, ev.IdempotencyKey, beats[i].RawEventID)
		}
		if ev.PartitionKey != "domestic-disturbance" {
			t.Errorf("event[%d] partition = %q", i, ev.PartitionKey)
		}
	}
}

// TestF1BeatExecutorIsRealEventLogPath proves BeatExecutor is a real Append
// driver (C2), not a cosmetic timer: N beats → N envelopes, ordered, no process
// bulk-seed. Composition wires the same Append shape via Controller.OnBeat.
func TestF1BeatExecutorIsRealEventLogPath(t *testing.T) {
	log := memory.New()
	beats := []contracts.ScheduledBeat{
		{BeatID: "a", Order: 1, RawEventID: "raw-a"},
		{BeatID: "b", Order: 2, RawEventID: "raw-b"},
		{BeatID: "c", Order: 3, RawEventID: "raw-c"},
	}
	src := simulation.BeatSourceFunc(func(_ context.Context, id string) ([]byte, error) {
		return []byte(`{"raw":"` + id + `"}`), nil
	})
	var appendOrder []string
	exec, err := simulation.NewBeatExecutor(simulation.ExecutorConfig{
		Schedule:     progressiveSchedule{beats: beats},
		Log:          log,
		Source:       src,
		BeatSpacing:  time.Millisecond,
		PartitionKey: "incident-f1",
		After: func(d time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Unix(0, 0).UTC()
			return ch
		},
		Clock: func() time.Time { return time.Unix(0, 0).UTC() },
		OnBeat: func(beat contracts.ScheduledBeat, env eventlog.EventEnvelope) {
			appendOrder = append(appendOrder, beat.BeatID)
			if env.IdempotencyKey != beat.RawEventID {
				t.Errorf("envelope key %q != raw %q", env.IdempotencyKey, beat.RawEventID)
			}
		},
	})
	if err != nil {
		t.Fatalf("NewBeatExecutor: %v", err)
	}
	if err := exec.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if log.Len() != 3 {
		t.Fatalf("appends = %d, want 3", log.Len())
	}
	if len(appendOrder) != 3 || appendOrder[0] != "a" || appendOrder[1] != "b" || appendOrder[2] != "c" {
		t.Fatalf("OnBeat order = %v, want [a b c]", appendOrder)
	}
}

// TestF1SessionIsolationActiveClearsEmptyBoard proves C3/D1h isolation at the
// session package: End clears Active; a second session gets a new id; no
// residual "active" pointer leaks the prior epoch.
func TestF1SessionIsolationActiveClearsEmptyBoard(t *testing.T) {
	beats := []contracts.ScheduledBeat{
		{BeatID: "only", Order: 1, RawEventID: "raw-only"},
	}
	active := session.NewActiveSession()
	var processCount int
	ctrl, err := session.New(session.Config{
		Schedule: progressiveSchedule{beats: beats},
		Active:   active,
		OnBeat: func(_ context.Context, _ contracts.ScheduledBeat) error {
			processCount++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	first, err := ctrl.Start(context.Background())
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	waitCtrlEnded(t, ctrl, 2*time.Second)
	if id, ok := active.ActiveSessionID(); !ok || id != first.SessionID {
		t.Fatalf("active after natural end = %q ok=%v, want %q", id, ok, first.SessionID)
	}

	// Explicit End → empty board policy (no active epoch).
	if _, err := ctrl.End(context.Background()); err != nil {
		t.Fatalf("End: %v", err)
	}
	if _, ok := active.ActiveSessionID(); ok {
		t.Fatal("active still set after End; empty board requires Clear")
	}

	// Second Play: new session id; process runs again (no cross-session skip).
	second, err := ctrl.Start(context.Background())
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if second.SessionID == "" || second.SessionID == first.SessionID {
		t.Fatalf("second session id = %q, first = %q; want distinct epoch", second.SessionID, first.SessionID)
	}
	waitCtrlEnded(t, ctrl, 2*time.Second)
	if processCount != 2 {
		t.Fatalf("processCount = %d, want 2 (one per session)", processCount)
	}
	if id, ok := active.ActiveSessionID(); !ok || id != second.SessionID {
		t.Fatalf("active after second natural end = %q ok=%v, want %q", id, ok, second.SessionID)
	}
}

func waitCtrlEnded(t *testing.T, ctrl *session.Controller, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctrl.Status().Status == contracts.SessionEnded {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("session status = %q, want ended", ctrl.Status().Status)
}
