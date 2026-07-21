package pgstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/eventlog"
)

func TestTopicChannelSanitization(t *testing.T) {
	t.Parallel()

	cases := []struct {
		topic string
		want  string
	}{
		{topic: "cop.updated", want: "mb_cop_updated"},
		{topic: "advisory.updated", want: "mb_advisory_updated"},
		{topic: "COP.Updated", want: "mb_cop_updated"},
		{topic: "  cop.updated  ", want: "mb_cop_updated"},
		{topic: "a-b_c", want: "mb_a_b_c"},
		{topic: "simple", want: "mb_simple"},
	}
	for _, tc := range cases {
		t.Run(tc.topic, func(t *testing.T) {
			got, err := TopicChannel(tc.topic)
			if err != nil {
				t.Fatalf("TopicChannel(%q): %v", tc.topic, err)
			}
			if got != tc.want {
				t.Fatalf("TopicChannel(%q) = %q, want %q", tc.topic, got, tc.want)
			}
			if len(got) > postgresChannelNameMax {
				t.Fatalf("channel %q exceeds %d bytes", got, postgresChannelNameMax)
			}
			for _, r := range got {
				if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
					t.Fatalf("channel %q has illegal rune %q", got, r)
				}
			}
		})
	}
}

func TestTopicChannelEmptyRejected(t *testing.T) {
	t.Parallel()
	for _, topic := range []string{"", "   ", "\t"} {
		_, err := TopicChannel(topic)
		if !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("TopicChannel(%q) error = %v, want ErrInvalidRecord", topic, err)
		}
	}
}

func TestTopicChannelHashFallback(t *testing.T) {
	t.Parallel()

	// Separators-only collapses to empty body → hash form.
	got, err := TopicChannel("...")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, hashChannelPrefix) {
		t.Fatalf("got %q, want prefix %q", got, hashChannelPrefix)
	}
	if len(got) > postgresChannelNameMax {
		t.Fatalf("hash channel too long: %d", len(got))
	}

	// Over-long topic after sanitize also hashes.
	long := strings.Repeat("a", postgresChannelNameMax)
	gotLong, err := TopicChannel(long)
	if err != nil {
		t.Fatal(err)
	}
	// "mb_" + 63 a's exceeds limit → hash.
	if !strings.HasPrefix(gotLong, hashChannelPrefix) {
		t.Fatalf("long topic channel = %q, want hash form", gotLong)
	}

	// Hash is stable.
	again, err := TopicChannel("...")
	if err != nil {
		t.Fatal(err)
	}
	if again != got {
		t.Fatalf("hash not stable: %q vs %q", got, again)
	}
}

func TestTopicChannelDistinctTopics(t *testing.T) {
	t.Parallel()
	a, err := TopicChannel("cop.updated")
	if err != nil {
		t.Fatal(err)
	}
	b, err := TopicChannel("advisory.updated")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("distinct topics mapped to same channel %q", a)
	}
}

func TestTopicChannelPunctuationCollisionDocumented(t *testing.T) {
	// Documents sanitize collision: "a.b" and "a_b" share one channel.
	t.Parallel()
	a, err := TopicChannel("a.b")
	if err != nil {
		t.Fatal(err)
	}
	b, err := TopicChannel("a_b")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("expected sanitize collision a.b / a_b → same channel; got %q vs %q", a, b)
	}
	if a != "mb_a_b" {
		t.Fatalf("channel = %q, want mb_a_b", a)
	}
}

func newTestEventBus(t *testing.T) *EventBus {
	t.Helper()
	s := newTestStore(t)
	return NewEventBus(s.Pool())
}

func TestEventBusPublishSubscribe(t *testing.T) {
	ctx := context.Background()
	bus := newTestEventBus(t)

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	notes, err := bus.Subscribe(subCtx, "cop.updated")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Give the LISTEN session a moment to be active on the server.
	waitForListenReady(t, bus, "cop.updated")

	want := []byte(`{"revision":7}`)
	if err := bus.Publish(ctx, "cop.updated", want); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got := recvNote(t, notes, 3*time.Second)
	if string(got) != string(want) {
		t.Fatalf("note = %q, want %q", got, want)
	}
}

func TestEventBusSlowSubscriberDoesNotBlockPublish(t *testing.T) {
	ctx := context.Background()
	bus := newTestEventBus(t)

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	notes, err := bus.Subscribe(subCtx, "cop.updated")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	waitForListenReady(t, bus, "cop.updated")

	// Flood publishes without draining. Publish must not hang.
	const n = 200
	done := make(chan error, 1)
	go func() {
		var last error
		for i := 0; i < n; i++ {
			if err := bus.Publish(ctx, "cop.updated", []byte("x")); err != nil {
				last = err
				break
			}
		}
		done <- last
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Publish under backpressure: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked under slow subscriber")
	}

	// Drain whatever arrived; channel must remain usable until cancel.
	cancel()
	drainUntilClosed(t, notes, 2*time.Second)
}

func TestEventBusSubscribeCancelClosesChannel(t *testing.T) {
	ctx := context.Background()
	bus := newTestEventBus(t)

	subCtx, cancel := context.WithCancel(ctx)
	notes, err := bus.Subscribe(subCtx, "cop.updated")
	if err != nil {
		cancel()
		t.Fatalf("Subscribe: %v", err)
	}

	cancel()
	drainUntilClosed(t, notes, 2*time.Second)
}

func TestEventBusOversizedPayloadRejected(t *testing.T) {
	ctx := context.Background()
	bus := newTestEventBus(t)

	tooBig := make([]byte, maxNotifyPayloadBytes+1)
	for i := range tooBig {
		tooBig[i] = 'a'
	}
	err := bus.Publish(ctx, "cop.updated", tooBig)
	if !errors.Is(err, ErrNotifyPayloadTooLarge) {
		t.Fatalf("error = %v, want ErrNotifyPayloadTooLarge", err)
	}

	// Boundary: exactly maxNotifyPayloadBytes is accepted.
	exact := make([]byte, maxNotifyPayloadBytes)
	for i := range exact {
		exact[i] = 'b'
	}
	if err := bus.Publish(ctx, "cop.updated", exact); err != nil {
		t.Fatalf("max-size publish: %v", err)
	}
}

func TestEventBusTopicsDoNotCrossTalk(t *testing.T) {
	ctx := context.Background()
	bus := newTestEventBus(t)

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	aNotes, err := bus.Subscribe(subCtx, "cop.updated")
	if err != nil {
		t.Fatalf("Subscribe A: %v", err)
	}
	bNotes, err := bus.Subscribe(subCtx, "advisory.updated")
	if err != nil {
		t.Fatalf("Subscribe B: %v", err)
	}
	waitForListenReady(t, bus, "cop.updated")
	waitForListenReady(t, bus, "advisory.updated")

	if err := bus.Publish(ctx, "cop.updated", []byte("from-a")); err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(ctx, "advisory.updated", []byte("from-b")); err != nil {
		t.Fatal(err)
	}

	gotA := recvNote(t, aNotes, 3*time.Second)
	gotB := recvNote(t, bNotes, 3*time.Second)
	if string(gotA) != "from-a" {
		t.Fatalf("topic A got %q, want from-a", gotA)
	}
	if string(gotB) != "from-b" {
		t.Fatalf("topic B got %q, want from-b", gotB)
	}

	// Ensure no late cross-talk lands on the other channel.
	assertNoNote(t, aNotes, 200*time.Millisecond)
	assertNoNote(t, bNotes, 200*time.Millisecond)
}

func TestEventBusImplementsInterface(t *testing.T) {
	t.Parallel()
	var _ eventlog.EventBus = (*EventBus)(nil)
}

// waitForListenReady yields after Subscribe so the WaitForNotification
// goroutine is scheduled. Subscribe only returns after LISTEN succeeds, so the
// session is already registered server-side.
func waitForListenReady(t *testing.T, _ *EventBus, _ string) {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
}

func recvNote(t *testing.T, notes <-chan []byte, timeout time.Duration) []byte {
	t.Helper()
	select {
	case note, ok := <-notes:
		if !ok {
			t.Fatal("notes channel closed before receiving")
		}
		return note
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for note after %s", timeout)
		return nil
	}
}

func assertNoNote(t *testing.T, notes <-chan []byte, wait time.Duration) {
	t.Helper()
	select {
	case note, ok := <-notes:
		if !ok {
			return
		}
		t.Fatalf("unexpected note %q", note)
	case <-time.After(wait):
	}
}

func drainUntilClosed(t *testing.T, notes <-chan []byte, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case _, ok := <-notes:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for notes channel to close")
		}
	}
}
