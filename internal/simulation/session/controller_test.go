package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/contracts"
)

type scheduleFixture struct {
	beats []contracts.ScheduledBeat
}

func (s scheduleFixture) Beats() []contracts.ScheduledBeat {
	out := make([]contracts.ScheduledBeat, len(s.beats))
	copy(out, s.beats)
	return out
}

func testBeats() []contracts.ScheduledBeat {
	return []contracts.ScheduledBeat{
		{BeatID: "beat-b", Order: 2, RawEventID: "raw-b", Delay: 0},
		{BeatID: "beat-a", Order: 1, RawEventID: "raw-a", Delay: 0},
		{BeatID: "beat-c", Order: 3, RawEventID: "raw-c", Delay: 0},
	}
}

func newTestController(t *testing.T, beats []contracts.ScheduledBeat, opts ...func(*Config)) *Controller {
	t.Helper()
	cfg := Config{
		Schedule: scheduleFixture{beats: beats},
		Clock:    func() time.Time { return time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC) },
		// Zero-delay After for deterministic unit tests without real sleeps.
		After: func(d time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
			return ch
		},
		NewSessionID: func() string { return "session-fixed" },
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	ctrl, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ctrl
}

func collectUntil(t *testing.T, sub contracts.SimulationStreamSubscription, n int, timeout time.Duration) []contracts.SimulationStreamEvent {
	t.Helper()
	out := make([]contracts.SimulationStreamEvent, 0, n)
	deadline := time.After(timeout)
	for len(out) < n {
		select {
		case ev, ok := <-sub.Events():
			if !ok {
				t.Fatalf("subscription closed after %d events, want %d", len(out), n)
			}
			out = append(out, ev)
		case <-deadline:
			t.Fatalf("timeout waiting for %d events; got %d: %#v", n, len(out), out)
		}
	}
	return out
}

func typesOf(events []contracts.SimulationStreamEvent) []contracts.StreamEventType {
	out := make([]contracts.StreamEventType, len(events))
	for i, ev := range events {
		out[i] = ev.Type
	}
	return out
}

func TestNewRequiresSchedule(t *testing.T) {
	_, err := New(Config{})
	if err != ErrNilSchedule {
		t.Fatalf("New() err = %v, want ErrNilSchedule", err)
	}
}

func TestStatusPendingBeforeStart(t *testing.T) {
	ctrl := newTestController(t, testBeats())
	status := ctrl.Status()
	if status.Status != contracts.SessionPending {
		t.Fatalf("status = %q, want pending", status.Status)
	}
	if status.SessionID != "" {
		t.Fatalf("session id before start = %q, want empty", status.SessionID)
	}
	if len(status.Beats) != 3 {
		t.Fatalf("beats = %d, want 3", len(status.Beats))
	}
}

func TestStartEmitsWorkspaceClearThenBeatsInOrder(t *testing.T) {
	ctrl := newTestController(t, testBeats())
	sub := ctrl.Subscribe()
	defer sub.Cancel()

	session, err := ctrl.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if session.Status != contracts.SessionRunning {
		t.Fatalf("start status = %q, want running", session.Status)
	}
	if session.SessionID != "session-fixed" {
		t.Fatalf("session id = %q", session.SessionID)
	}

	// workspace_clear, status_change(running), 3 beats, status_change(ended)
	events := collectUntil(t, sub, 6, time.Second)
	wantTypes := []contracts.StreamEventType{
		contracts.StreamEventWorkspaceClear,
		contracts.StreamEventStatusChange,
		contracts.StreamEventBeat,
		contracts.StreamEventBeat,
		contracts.StreamEventBeat,
		contracts.StreamEventStatusChange,
	}
	if got := typesOf(events); !equalTypes(got, wantTypes) {
		t.Fatalf("event types = %v, want %v", got, wantTypes)
	}

	// Sequence is monotonic per session.
	for i, ev := range events {
		if ev.Sequence != int64(i+1) {
			t.Fatalf("event[%d].Sequence = %d, want %d", i, ev.Sequence, i+1)
		}
		if ev.SessionID != "session-fixed" {
			t.Fatalf("event[%d].SessionID = %q", i, ev.SessionID)
		}
	}

	// Wait for auto-end.
	waitStatus(t, ctrl, contracts.SessionEnded, time.Second)
}

func TestBeatOrderMatchesScheduleOrderField(t *testing.T) {
	// Intentionally shuffled slice order vs Order field.
	beats := []contracts.ScheduledBeat{
		{BeatID: "third", Order: 30, RawEventID: "r3", Delay: 0},
		{BeatID: "first", Order: 10, RawEventID: "r1", Delay: 0},
		{BeatID: "second", Order: 20, RawEventID: "r2", Delay: 0},
	}
	ctrl := newTestController(t, beats)
	sub := ctrl.Subscribe()
	defer sub.Cancel()

	if _, err := ctrl.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	events := collectUntil(t, sub, 6, time.Second)

	var beatIDs []string
	var orders []int
	for _, ev := range events {
		if ev.Type != contracts.StreamEventBeat {
			continue
		}
		payload, ok := ev.Payload.(map[string]any)
		if !ok {
			t.Fatalf("beat payload type %T", ev.Payload)
		}
		beatIDs = append(beatIDs, payload["beat_id"].(string))
		orders = append(orders, payload["order"].(int))
		if payload["raw_event_id"] == "" {
			t.Fatal("beat payload missing raw_event_id")
		}
	}
	wantIDs := []string{"first", "second", "third"}
	wantOrders := []int{10, 20, 30}
	if !equalStrings(beatIDs, wantIDs) {
		t.Fatalf("beat ids = %v, want %v", beatIDs, wantIDs)
	}
	if !equalInts(orders, wantOrders) {
		t.Fatalf("beat orders = %v, want %v", orders, wantOrders)
	}
}

func TestEndTransitionsToEndedAndStopsFurtherBeats(t *testing.T) {
	clock := NewVirtualClock(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	beats := []contracts.ScheduledBeat{
		{BeatID: "early", Order: 1, RawEventID: "r1", Delay: 0},
		{BeatID: "late", Order: 2, RawEventID: "r2", Delay: 5 * time.Second},
	}
	ctrl := newTestController(t, beats, func(cfg *Config) {
		cfg.Clock = clock.Now
		cfg.After = clock.After
	})
	sub := ctrl.Subscribe()
	defer sub.Cancel()

	if _, err := ctrl.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// workspace_clear, status running, early beat
	events := collectUntil(t, sub, 3, time.Second)
	if events[2].Type != contracts.StreamEventBeat {
		t.Fatalf("third event = %v, want beat", events[2].Type)
	}
	payload := events[2].Payload.(map[string]any)
	if payload["beat_id"] != "early" {
		t.Fatalf("beat_id = %v, want early", payload["beat_id"])
	}

	session, err := ctrl.End(context.Background())
	if err != nil {
		t.Fatalf("End: %v", err)
	}
	if session.Status != contracts.SessionEnded {
		t.Fatalf("end status = %q, want ended", session.Status)
	}

	// Allow any status_change from End.
	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case ev := <-sub.Events():
			if ev.Type == contracts.StreamEventBeat {
				t.Fatalf("received beat after End: %#v", ev)
			}
		case <-deadline:
			goto done
		}
	}
done:

	// Advancing the clock must not deliver the late beat.
	clock.Advance(10 * time.Second)
	select {
	case ev := <-sub.Events():
		if ev.Type == contracts.StreamEventBeat {
			t.Fatalf("late beat emitted after End: %#v", ev)
		}
	case <-time.After(50 * time.Millisecond):
	}

	if got := ctrl.Status(); got.Status != contracts.SessionEnded {
		t.Fatalf("status after end = %q", got.Status)
	}
}

func TestResetCreatesNewSessionIDAndCleanStream(t *testing.T) {
	var idMu sync.Mutex
	ids := []string{"session-one", "session-two"}
	next := 0
	newID := func() string {
		idMu.Lock()
		defer idMu.Unlock()
		if next >= len(ids) {
			return "session-extra"
		}
		id := ids[next]
		next++
		return id
	}

	ctrl := newTestController(t, testBeats(), func(cfg *Config) {
		cfg.NewSessionID = newID
	})
	sub := ctrl.Subscribe()
	defer sub.Cancel()

	first, err := ctrl.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if first.SessionID != "session-one" {
		t.Fatalf("first id = %q", first.SessionID)
	}
	firstEvents := collectUntil(t, sub, 6, time.Second)
	waitStatus(t, ctrl, contracts.SessionEnded, time.Second)

	// Snapshot of first session remains queryable (ended, same id).
	beforeReset := ctrl.Status()
	if beforeReset.SessionID != "session-one" || beforeReset.Status != contracts.SessionEnded {
		t.Fatalf("before reset = %#v", beforeReset)
	}

	second, err := ctrl.Reset(context.Background())
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if second.SessionID != "session-two" {
		t.Fatalf("second id = %q, want session-two", second.SessionID)
	}
	if second.SessionID == first.SessionID {
		t.Fatal("reset reused session id")
	}
	if second.Status != contracts.SessionRunning {
		t.Fatalf("reset status = %q, want running", second.Status)
	}

	// New session stream starts clean: workspace_clear first, sequence restarts at 1.
	secondEvents := collectUntil(t, sub, 6, time.Second)
	if secondEvents[0].Type != contracts.StreamEventWorkspaceClear {
		t.Fatalf("reset first event = %v, want workspace_clear", secondEvents[0].Type)
	}
	if secondEvents[0].Sequence != 1 {
		t.Fatalf("reset sequence start = %d, want 1", secondEvents[0].Sequence)
	}
	for _, ev := range secondEvents {
		if ev.SessionID != "session-two" {
			t.Fatalf("reset stream event session = %q, want session-two", ev.SessionID)
		}
	}

	// Prior session events are not re-emitted as a replay of immutable history:
	// first session beat identities appear only in firstEvents, and the second
	// session has its own fresh workspace_clear (not an append of first history).
	if len(firstEvents) != 6 || len(secondEvents) != 6 {
		t.Fatalf("event counts first=%d second=%d", len(firstEvents), len(secondEvents))
	}
	// Controller holds no durable store; Status after second session no longer
	// claims session-one as current — prior id is not rewritten in place.
	if got := ctrl.Status(); got.SessionID == "session-one" {
		t.Fatal("status still claims prior session after reset")
	}
}

func TestDeterministicTimingWithVirtualClock(t *testing.T) {
	clock := NewVirtualClock(time.Date(2026, 7, 20, 15, 0, 0, 0, time.UTC))
	beats := []contracts.ScheduledBeat{
		{BeatID: "t0", Order: 1, RawEventID: "r0", Delay: 0},
		{BeatID: "t5", Order: 2, RawEventID: "r5", Delay: 5 * time.Second},
	}
	ctrl := newTestController(t, beats, func(cfg *Config) {
		cfg.Clock = clock.Now
		cfg.After = clock.After
		cfg.NewSessionID = func() string { return "timed-session" }
	})
	sub := ctrl.Subscribe()
	defer sub.Cancel()

	if _, err := ctrl.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Immediate events: clear, status, first beat. Delayed beat must wait.
	events := collectUntil(t, sub, 3, time.Second)
	if events[2].Type != contracts.StreamEventBeat {
		t.Fatalf("third = %v", events[2].Type)
	}
	if events[2].Payload.(map[string]any)["beat_id"] != "t0" {
		t.Fatalf("unexpected first beat payload %#v", events[2].Payload)
	}

	select {
	case ev := <-sub.Events():
		t.Fatalf("unexpected event before Advance: %#v", ev)
	case <-time.After(30 * time.Millisecond):
	}

	clock.Advance(5 * time.Second)
	rest := collectUntil(t, sub, 2, time.Second) // beat t5 + status ended
	if rest[0].Type != contracts.StreamEventBeat {
		t.Fatalf("after advance type = %v, want beat", rest[0].Type)
	}
	if rest[0].Payload.(map[string]any)["beat_id"] != "t5" {
		t.Fatalf("delayed beat = %#v", rest[0].Payload)
	}
	if rest[1].Type != contracts.StreamEventStatusChange {
		t.Fatalf("final event = %v, want status_change", rest[1].Type)
	}
	waitStatus(t, ctrl, contracts.SessionEnded, time.Second)
}

func TestEqualSpacingIgnoresFixtureDelay(t *testing.T) {
	// BeatSpacing > 0: fire at i*spacing; fixture Delays (all 100ms) ignored.
	clock := NewVirtualClock(time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC))
	spacing := 2 * time.Second
	beats := []contracts.ScheduledBeat{
		{BeatID: "a", Order: 1, RawEventID: "r1", Delay: 100 * time.Millisecond},
		{BeatID: "b", Order: 2, RawEventID: "r2", Delay: 100 * time.Millisecond},
		{BeatID: "c", Order: 3, RawEventID: "r3", Delay: 100 * time.Millisecond},
	}
	ctrl := newTestController(t, beats, func(cfg *Config) {
		cfg.Clock = clock.Now
		cfg.After = clock.After
		cfg.BeatSpacing = spacing
		cfg.NewSessionID = func() string { return "spaced-session" }
	})
	sub := ctrl.Subscribe()
	defer sub.Cancel()

	if _, err := ctrl.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// clear, status, first beat (index 0 → delay 0)
	events := collectUntil(t, sub, 3, time.Second)
	if events[2].Payload.(map[string]any)["beat_id"] != "a" {
		t.Fatalf("first beat = %#v", events[2].Payload)
	}

	// Relative-to-start fixture delays would fire b and c at ~100ms; equal
	// spacing must still be waiting for 2s.
	select {
	case ev := <-sub.Events():
		t.Fatalf("unexpected event before Advance (flood?): %#v", ev)
	case <-time.After(30 * time.Millisecond):
	}

	clock.Advance(spacing)
	second := collectUntil(t, sub, 1, time.Second)
	if second[0].Payload.(map[string]any)["beat_id"] != "b" {
		t.Fatalf("second beat = %#v", second[0].Payload)
	}

	select {
	case ev := <-sub.Events():
		t.Fatalf("unexpected third beat before second Advance: %#v", ev)
	case <-time.After(30 * time.Millisecond):
	}

	clock.Advance(spacing)
	rest := collectUntil(t, sub, 2, time.Second) // beat c + ended
	if rest[0].Payload.(map[string]any)["beat_id"] != "c" {
		t.Fatalf("third beat = %#v", rest[0].Payload)
	}
	waitStatus(t, ctrl, contracts.SessionEnded, time.Second)
}

func TestStatusReflectsLifecycle(t *testing.T) {
	ctrl := newTestController(t, []contracts.ScheduledBeat{
		{BeatID: "only", Order: 1, RawEventID: "r1", Delay: 0},
	})

	if got := ctrl.Status().Status; got != contracts.SessionPending {
		t.Fatalf("initial = %q", got)
	}

	// Hold the delayed second beat so we can observe running.
	clock := NewVirtualClock(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	ctrl = newTestController(t, []contracts.ScheduledBeat{
		{BeatID: "only", Order: 1, RawEventID: "r1", Delay: time.Hour},
	}, func(cfg *Config) {
		cfg.Clock = clock.Now
		cfg.After = clock.After
	})
	if _, err := ctrl.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := ctrl.Status().Status; got != contracts.SessionRunning {
		t.Fatalf("running = %q", got)
	}

	if _, err := ctrl.End(context.Background()); err != nil {
		t.Fatalf("End: %v", err)
	}
	if got := ctrl.Status().Status; got != contracts.SessionEnded {
		t.Fatalf("ended = %q", got)
	}
	// Completed session remains queryable.
	if got := ctrl.Status(); got.SessionID == "" || len(got.Beats) != 1 {
		t.Fatalf("queryable ended session = %#v", got)
	}
}

func TestConcurrentSubscribeReceivesActiveSessionOnly(t *testing.T) {
	var idMu sync.Mutex
	next := 0
	ctrl := newTestController(t, testBeats(), func(cfg *Config) {
		cfg.NewSessionID = func() string {
			idMu.Lock()
			defer idMu.Unlock()
			next++
			return "sess-" + itoa(next)
		}
	})

	sub1 := ctrl.Subscribe()
	defer sub1.Cancel()
	sub2 := ctrl.Subscribe()
	defer sub2.Cancel()

	session, err := ctrl.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	var wg sync.WaitGroup
	collect := func(sub contracts.SimulationStreamSubscription) []contracts.SimulationStreamEvent {
		defer wg.Done()
		return collectUntil(t, sub, 6, time.Second)
	}
	wg.Add(2)
	var e1, e2 []contracts.SimulationStreamEvent
	go func() { e1 = collect(sub1) }()
	go func() { e2 = collect(sub2) }()
	wg.Wait()

	for _, events := range [][]contracts.SimulationStreamEvent{e1, e2} {
		for _, ev := range events {
			if ev.SessionID != session.SessionID {
				t.Fatalf("event session %q != active %q", ev.SessionID, session.SessionID)
			}
		}
	}

	// Concurrent Status reads during run/end are safe.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			_ = ctrl.Status()
		}
	}()
	waitStatus(t, ctrl, contracts.SessionEnded, time.Second)
	<-done
}

func TestStartWhileRunningFails(t *testing.T) {
	clock := NewVirtualClock(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	ctrl := newTestController(t, []contracts.ScheduledBeat{
		{BeatID: "slow", Order: 1, RawEventID: "r1", Delay: time.Hour},
	}, func(cfg *Config) {
		cfg.Clock = clock.Now
		cfg.After = clock.After
	})
	if _, err := ctrl.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := ctrl.Start(context.Background()); err != ErrAlreadyRunning {
		t.Fatalf("second Start err = %v, want ErrAlreadyRunning", err)
	}
	if _, err := ctrl.End(context.Background()); err != nil {
		t.Fatalf("End: %v", err)
	}
}

func TestSlowSubscriberDoesNotBlockController(t *testing.T) {
	// Tiny buffer forces drop-oldest; controller must still complete.
	ctrl := newTestController(t, testBeats(), func(cfg *Config) {
		cfg.SubscriberBuffer = 1
	})
	sub := ctrl.Subscribe()
	defer sub.Cancel()

	// Never read from sub until after completion.
	if _, err := ctrl.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitStatus(t, ctrl, contracts.SessionEnded, time.Second)

	// Drain whatever remains (at most buffer capacity of recent events).
	drained := 0
drain:
	for {
		select {
		case <-sub.Events():
			drained++
		default:
			break drain
		}
	}
	if drained == 0 {
		t.Fatal("expected some events retained for slow subscriber")
	}
}

func TestStatusCopyIsIndependent(t *testing.T) {
	ctrl := newTestController(t, testBeats())
	status := ctrl.Status()
	status.Beats[0].BeatID = "mutated"
	status.Status = contracts.SessionEnded
	again := ctrl.Status()
	if again.Beats[0].BeatID == "mutated" {
		t.Fatal("Status returned live slice")
	}
	if again.Status != contracts.SessionPending {
		t.Fatalf("Status mutated controller state: %q", again.Status)
	}
}

func TestOnBeatInvokedInOrderAfterEachEmission(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	ctrl := newTestController(t, testBeats(), func(cfg *Config) {
		cfg.OnBeat = func(_ context.Context, beat contracts.ScheduledBeat) error {
			mu.Lock()
			seen = append(seen, beat.BeatID)
			mu.Unlock()
			return nil
		}
	})
	if _, err := ctrl.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitStatus(t, ctrl, contracts.SessionEnded, time.Second)
	mu.Lock()
	defer mu.Unlock()
	// testBeats order fields: beat-a(1), beat-b(2), beat-c(3)
	want := []string{"beat-a", "beat-b", "beat-c"}
	if !equalStrings(seen, want) {
		t.Fatalf("OnBeat order = %v, want %v", seen, want)
	}
}

func TestOnBeatErrorStopsFurtherBeatsAndEndsSession(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	ctrl := newTestController(t, testBeats(), func(cfg *Config) {
		cfg.OnBeat = func(_ context.Context, beat contracts.ScheduledBeat) error {
			mu.Lock()
			seen = append(seen, beat.BeatID)
			mu.Unlock()
			if beat.BeatID == "beat-a" {
				return context.Canceled
			}
			return nil
		}
	})
	if _, err := ctrl.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitStatus(t, ctrl, contracts.SessionEnded, time.Second)
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != "beat-a" {
		t.Fatalf("OnBeat seen = %v, want only beat-a", seen)
	}
}

func waitStatus(t *testing.T, ctrl *Controller, want contracts.SessionStatus, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctrl.Status().Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("status = %q, want %q", ctrl.Status().Status, want)
}

func equalTypes(a, b []contracts.StreamEventType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
