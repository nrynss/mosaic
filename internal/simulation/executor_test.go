package simulation_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/eventlog"
	"mosaic.local/mosaic/internal/simulation"
	"mosaic.local/mosaic/internal/simulation/session"
)

type scheduleFixture struct {
	beats []contracts.ScheduledBeat
}

func (s scheduleFixture) Beats() []contracts.ScheduledBeat {
	out := make([]contracts.ScheduledBeat, len(s.beats))
	copy(out, s.beats)
	return out
}

// recordingLog is a fake eventlog.EventLog that records Append order.
type recordingLog struct {
	mu        sync.Mutex
	envelopes []eventlog.EventEnvelope
	err       error
	// delayBeforeAppend optionally blocks each Append (not used by default).
	onAppend func(eventlog.EventEnvelope)
}

func (r *recordingLog) Append(_ context.Context, e eventlog.EventEnvelope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	// Copy payload so callers cannot mutate recorded history.
	payload := append([]byte(nil), e.Payload...)
	env := eventlog.EventEnvelope{
		PartitionKey:   e.PartitionKey,
		IdempotencyKey: e.IdempotencyKey,
		Type:           e.Type,
		Payload:        payload,
	}
	r.envelopes = append(r.envelopes, env)
	if r.onAppend != nil {
		r.onAppend(env)
	}
	return nil
}

func (r *recordingLog) snapshot() []eventlog.EventEnvelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]eventlog.EventEnvelope, len(r.envelopes))
	copy(out, r.envelopes)
	return out
}

func mapSource(payloads map[string][]byte) simulation.BeatSource {
	return simulation.BeatSourceFunc(func(_ context.Context, rawEventID string) ([]byte, error) {
		p, ok := payloads[rawEventID]
		if !ok {
			return nil, errors.New("unknown raw event: " + rawEventID)
		}
		return append([]byte(nil), p...), nil
	})
}

func threeBeats() []contracts.ScheduledBeat {
	// Shuffled slice order; Order field dictates fire order.
	return []contracts.ScheduledBeat{
		{BeatID: "b", Order: 2, RawEventID: "raw-b", Delay: 100 * time.Millisecond},
		{BeatID: "a", Order: 1, RawEventID: "raw-a", Delay: 0},
		{BeatID: "c", Order: 3, RawEventID: "raw-c", Delay: 100 * time.Millisecond},
	}
}

func TestBeatExecutorAppendsOneEnvelopePerBeatInOrder(t *testing.T) {
	log := &recordingLog{}
	src := mapSource(map[string][]byte{
		"raw-a": []byte(`{"id":"a"}`),
		"raw-b": []byte(`{"id":"b"}`),
		"raw-c": []byte(`{"id":"c"}`),
	})
	// Immediate After so Run does not wall-clock sleep.
	var callbacks []string
	exec, err := simulation.NewBeatExecutor(simulation.ExecutorConfig{
		Schedule:     scheduleFixture{beats: threeBeats()},
		Log:          log,
		Source:       src,
		BeatSpacing:  time.Second,
		PartitionKey: "incident-test-001",
		After: func(d time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Unix(0, 0).UTC()
			return ch
		},
		Clock: func() time.Time { return time.Unix(0, 0).UTC() },
		OnBeat: func(beat contracts.ScheduledBeat, env eventlog.EventEnvelope) {
			callbacks = append(callbacks, beat.BeatID)
			if env.IdempotencyKey != beat.RawEventID {
				t.Errorf("callback envelope key %q != raw %q", env.IdempotencyKey, beat.RawEventID)
			}
		},
	})
	if err != nil {
		t.Fatalf("NewBeatExecutor: %v", err)
	}

	if err := exec.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := log.snapshot()
	if len(got) != 3 {
		t.Fatalf("appends = %d, want 3", len(got))
	}
	wantIDs := []string{"raw-a", "raw-b", "raw-c"}
	wantPayloads := []string{`{"id":"a"}`, `{"id":"b"}`, `{"id":"c"}`}
	for i, env := range got {
		if env.PartitionKey != "incident-test-001" {
			t.Errorf("[%d] PartitionKey = %q", i, env.PartitionKey)
		}
		if env.IdempotencyKey != wantIDs[i] {
			t.Errorf("[%d] IdempotencyKey = %q, want %q", i, env.IdempotencyKey, wantIDs[i])
		}
		if env.Type != simulation.EventTypeRawEvent {
			t.Errorf("[%d] Type = %q, want %q", i, env.Type, simulation.EventTypeRawEvent)
		}
		if string(env.Payload) != wantPayloads[i] {
			t.Errorf("[%d] Payload = %s, want %s", i, env.Payload, wantPayloads[i])
		}
	}
	if len(callbacks) != 3 || callbacks[0] != "a" || callbacks[1] != "b" || callbacks[2] != "c" {
		t.Fatalf("OnBeat order = %v, want [a b c]", callbacks)
	}
}

func TestBeatExecutorEqualSpacingCumulativeWithVirtualClock(t *testing.T) {
	// Prove waits are index*spacing (cumulative ladder), NOT all relative to
	// the same small fixture Delay (the flood bug).
	clock := session.NewVirtualClock(time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC))
	log := &recordingLog{}
	src := mapSource(map[string][]byte{
		"raw-a": []byte("a"),
		"raw-b": []byte("b"),
		"raw-c": []byte("c"),
	})
	// Fixture delays are tiny and identical — equal spacing must ignore them.
	beats := []contracts.ScheduledBeat{
		{BeatID: "a", Order: 1, RawEventID: "raw-a", Delay: 100 * time.Millisecond},
		{BeatID: "b", Order: 2, RawEventID: "raw-b", Delay: 100 * time.Millisecond},
		{BeatID: "c", Order: 3, RawEventID: "raw-c", Delay: 100 * time.Millisecond},
	}
	spacing := 2 * time.Second
	var appendAt []time.Time
	log.onAppend = func(_ eventlog.EventEnvelope) {
		appendAt = append(appendAt, clock.Now())
	}

	exec, err := simulation.NewBeatExecutor(simulation.ExecutorConfig{
		Schedule:    scheduleFixture{beats: beats},
		Log:         log,
		Source:      src,
		BeatSpacing: spacing,
		Clock:       clock.Now,
		After:       clock.After,
	})
	if err != nil {
		t.Fatalf("NewBeatExecutor: %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- exec.Run(context.Background()) }()

	// Beat 0 is immediate.
	waitAppends(t, log, 1, time.Second)
	if len(log.snapshot()) != 1 {
		t.Fatalf("after t0: %d appends", len(log.snapshot()))
	}

	// Without advance, beat 1 must not fire (would if delays were all 100ms from start).
	select {
	case <-time.After(30 * time.Millisecond):
	}
	if n := len(log.snapshot()); n != 1 {
		t.Fatalf("before first advance: %d appends, want 1 (not flood)", n)
	}

	clock.Advance(spacing) // t = 2s → beat 1
	waitAppends(t, log, 2, time.Second)

	// Still waiting on beat 2 until 4s total.
	select {
	case <-time.After(30 * time.Millisecond):
	}
	if n := len(log.snapshot()); n != 2 {
		t.Fatalf("before second advance: %d appends, want 2", n)
	}

	clock.Advance(spacing) // t = 4s → beat 2
	waitAppends(t, log, 3, time.Second)

	if err := <-errCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(appendAt) != 3 {
		t.Fatalf("append timestamps = %d, want 3", len(appendAt))
	}
	// Cumulative: 0, 2s, 4s from start — not 100ms, 100ms, 100ms.
	start := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	wantOffsets := []time.Duration{0, 2 * time.Second, 4 * time.Second}
	for i, at := range appendAt {
		got := at.Sub(start)
		if got != wantOffsets[i] {
			t.Errorf("append[%d] offset = %v, want %v", i, got, wantOffsets[i])
		}
	}
}

func TestBeatExecutorBurstZeroDelay(t *testing.T) {
	log := &recordingLog{}
	n := 20
	beats := make([]contracts.ScheduledBeat, n)
	payloads := make(map[string][]byte, n)
	for i := 0; i < n; i++ {
		id := "raw-" + itoa(i)
		beats[i] = contracts.ScheduledBeat{
			BeatID:     "beat-" + itoa(i),
			Order:      i + 1,
			RawEventID: id,
			Delay:      time.Hour, // would hang without burst
		}
		payloads[id] = []byte(id)
	}

	// Real time.After with huge delays would block; burst must skip waits.
	// Use a frozen clock + After that panics if non-zero delay is requested.
	exec, err := simulation.NewBeatExecutor(simulation.ExecutorConfig{
		Schedule:    scheduleFixture{beats: beats},
		Log:         log,
		Source:      mapSource(payloads),
		Burst:       true,
		BeatSpacing: 10 * time.Second,
		Clock:       func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		After: func(d time.Duration) <-chan time.Time {
			if d > 0 {
				t.Errorf("burst mode must not wait; After(%v) called", d)
			}
			ch := make(chan time.Time, 1)
			ch <- time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			return ch
		},
	})
	if err != nil {
		t.Fatalf("NewBeatExecutor: %v", err)
	}

	// Wall-clock bound: no sleeps required.
	done := make(chan error, 1)
	go func() { done <- exec.Run(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("burst Run did not finish promptly (unexpected waits)")
	}

	if got := len(log.snapshot()); got != n {
		t.Fatalf("appends = %d, want %d", got, n)
	}
	for i, env := range log.snapshot() {
		want := "raw-" + itoa(i)
		if env.IdempotencyKey != want {
			t.Errorf("[%d] key = %q, want %q", i, env.IdempotencyKey, want)
		}
	}
}

func TestBeatExecutorUseScheduleDelaysRelativeToStart(t *testing.T) {
	clock := session.NewVirtualClock(time.Date(2026, 7, 21, 15, 0, 0, 0, time.UTC))
	log := &recordingLog{}
	src := mapSource(map[string][]byte{"raw-0": []byte("0"), "raw-5": []byte("5")})
	beats := []contracts.ScheduledBeat{
		{BeatID: "t0", Order: 1, RawEventID: "raw-0", Delay: 0},
		{BeatID: "t5", Order: 2, RawEventID: "raw-5", Delay: 5 * time.Second},
	}
	exec, err := simulation.NewBeatExecutor(simulation.ExecutorConfig{
		Schedule:          scheduleFixture{beats: beats},
		Log:               log,
		Source:            src,
		UseScheduleDelays: true,
		Clock:             clock.Now,
		After:             clock.After,
	})
	if err != nil {
		t.Fatalf("NewBeatExecutor: %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- exec.Run(context.Background()) }()

	waitAppends(t, log, 1, time.Second)
	select {
	case <-time.After(20 * time.Millisecond):
	}
	if len(log.snapshot()) != 1 {
		t.Fatal("second beat fired before Advance under schedule delays")
	}
	clock.Advance(5 * time.Second)
	waitAppends(t, log, 2, time.Second)
	if err := <-errCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestBeatExecutorRequiresDeps(t *testing.T) {
	beats := scheduleFixture{beats: threeBeats()}
	log := &recordingLog{}
	src := mapSource(map[string][]byte{"raw-a": []byte("a"), "raw-b": []byte("b"), "raw-c": []byte("c")})

	if _, err := simulation.NewBeatExecutor(simulation.ExecutorConfig{Log: log, Source: src}); err != simulation.ErrNilSchedule {
		t.Fatalf("nil schedule err = %v", err)
	}
	if _, err := simulation.NewBeatExecutor(simulation.ExecutorConfig{Schedule: beats, Source: src}); err != simulation.ErrNilEventLog {
		t.Fatalf("nil log err = %v", err)
	}
	if _, err := simulation.NewBeatExecutor(simulation.ExecutorConfig{Schedule: beats, Log: log}); err != simulation.ErrNilBeatSource {
		t.Fatalf("nil source err = %v", err)
	}
}

func TestBeatExecutorDefaultPartitionKeyAndSpacing(t *testing.T) {
	log := &recordingLog{}
	src := mapSource(map[string][]byte{"raw-a": []byte("a")})
	exec, err := simulation.NewBeatExecutor(simulation.ExecutorConfig{
		Schedule: scheduleFixture{beats: []contracts.ScheduledBeat{
			{BeatID: "a", Order: 1, RawEventID: "raw-a"},
		}},
		Log:    log,
		Source: src,
		// Zero spacing → DefaultBeatSpacing; Burst so we do not wait 2.5s.
		Burst: true,
	})
	if err != nil {
		t.Fatalf("NewBeatExecutor: %v", err)
	}
	if err := exec.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := log.snapshot()
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].PartitionKey != simulation.DefaultPartitionKey {
		t.Fatalf("PartitionKey = %q, want %q", got[0].PartitionKey, simulation.DefaultPartitionKey)
	}
}

func TestBeatExecutorMissingPayloadStops(t *testing.T) {
	log := &recordingLog{}
	src := mapSource(map[string][]byte{"raw-a": []byte("a")}) // missing raw-b
	exec, err := simulation.NewBeatExecutor(simulation.ExecutorConfig{
		Schedule: scheduleFixture{beats: []contracts.ScheduledBeat{
			{BeatID: "a", Order: 1, RawEventID: "raw-a"},
			{BeatID: "b", Order: 2, RawEventID: "raw-b"},
		}},
		Log:    log,
		Source: src,
		Burst:  true,
	})
	if err != nil {
		t.Fatalf("NewBeatExecutor: %v", err)
	}
	if err := exec.Run(context.Background()); err == nil {
		t.Fatal("expected error for missing payload")
	}
	if len(log.snapshot()) != 1 {
		t.Fatalf("partial appends = %d, want 1", len(log.snapshot()))
	}
}

func TestBeatExecutorCancelDuringWait(t *testing.T) {
	clock := session.NewVirtualClock(time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC))
	log := &recordingLog{}
	src := mapSource(map[string][]byte{"raw-a": []byte("a"), "raw-b": []byte("b")})
	exec, err := simulation.NewBeatExecutor(simulation.ExecutorConfig{
		Schedule: scheduleFixture{beats: []contracts.ScheduledBeat{
			{BeatID: "a", Order: 1, RawEventID: "raw-a"},
			{BeatID: "b", Order: 2, RawEventID: "raw-b"},
		}},
		Log:         log,
		Source:      src,
		BeatSpacing: time.Hour,
		Clock:       clock.Now,
		After:       clock.After,
	})
	if err != nil {
		t.Fatalf("NewBeatExecutor: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- exec.Run(ctx) }()
	waitAppends(t, log, 1, time.Second)
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if len(log.snapshot()) != 1 {
		t.Fatalf("appends after cancel = %d", len(log.snapshot()))
	}
}

func waitAppends(t *testing.T, log *recordingLog, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(log.snapshot()) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %d appends; got %d", n, len(log.snapshot()))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
