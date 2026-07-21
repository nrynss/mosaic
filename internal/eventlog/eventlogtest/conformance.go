package eventlogtest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/eventlog"
)

// Factory constructs an isolated backend instance for one subtest. The cleanup
// function must release resources (connections, topics, temp databases).
// EventBus may be nil only if the backend has no fan-out; EventBus suites are
// then skipped. Prefer always providing a bus so Publish/Subscribe is gated.
type Factory func() (eventlog.EventLog, eventlog.EventConsumer, eventlog.EventBus, func())

// SharedConsumersFactory builds one log and two consumers in the same consumer
// group on a shared backend. Required for the multi-worker partition test.
type SharedConsumersFactory func() (eventlog.EventLog, eventlog.EventConsumer, eventlog.EventConsumer, eventlog.EventBus, func())

// Option customizes RunConformanceTests.
type Option func(*suiteConfig)

type suiteConfig struct {
	shared SharedConsumersFactory
}

// WithSharedConsumers enables the multi-worker partition isolation test.
func WithSharedConsumers(f SharedConsumersFactory) Option {
	return func(c *suiteConfig) { c.shared = f }
}

// RunConformanceTests validates a transport backend against the EventLog,
// EventConsumer, and EventBus contracts. It is the E2 gate for any new backend
// (Postgres today; Kafka/Redpanda later).
func RunConformanceTests(t *testing.T, newStore Factory, opts ...Option) {
	t.Helper()
	var cfg suiteConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	t.Run("AppendAndConsume", func(t *testing.T) { testAppendAndConsume(t, newStore) })
	t.Run("Idempotency", func(t *testing.T) { testIdempotency(t, newStore) })
	t.Run("IdempotentFirstWinsPayload", func(t *testing.T) { testIdempotentFirstWinsPayload(t, newStore) })
	t.Run("PartitionKeyOrdering", func(t *testing.T) { testPartitionKeyOrdering(t, newStore) })
	t.Run("AtLeastOnceRedelivery", func(t *testing.T) { testAtLeastOnceRedelivery(t, newStore) })
	t.Run("PositionTokenIntegrity", func(t *testing.T) { testPositionTokenIntegrity(t, newStore) })
	t.Run("MultiWorkerPartitionIsolation", func(t *testing.T) {
		if cfg.shared == nil {
			t.Skip("WithSharedConsumers not configured")
		}
		testMultiWorkerPartitionIsolation(t, cfg.shared)
	})
	t.Run("EventBus", func(t *testing.T) {
		_, _, bus, cleanup := newStore()
		cleanup()
		if bus == nil {
			t.Skip("EventBus not provided by factory")
		}
		t.Run("PublishSubscribe", func(t *testing.T) { testEventBusPublishSubscribe(t, newStore) })
		t.Run("CancelClosesChannel", func(t *testing.T) { testEventBusCancelClosesChannel(t, newStore) })
		t.Run("TopicsIsolated", func(t *testing.T) { testEventBusTopicsIsolated(t, newStore) })
		t.Run("BackpressureDoesNotHang", func(t *testing.T) { testEventBusBackpressureDoesNotHang(t, newStore) })
	})
}

func testAppendAndConsume(t *testing.T, newStore Factory) {
	log, consumer, _, cleanup := newStore()
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	env := eventlog.EventEnvelope{
		PartitionKey:   "p1",
		IdempotencyKey: "i1",
		Type:           "t1",
		Payload:        []byte("pay1"),
	}
	if err := log.Append(ctx, env); err != nil {
		t.Fatal(err)
	}

	received := make(chan eventlog.Event, 1)
	go func() {
		_ = consumer.Run(ctx, func(c context.Context, e eventlog.Event) error {
			if e.PartitionKey == "p1" {
				select {
				case received <- e:
				default:
				}
			}
			return nil
		})
	}()

	select {
	case e := <-received:
		if e.PartitionKey != env.PartitionKey || e.IdempotencyKey != env.IdempotencyKey || e.Type != env.Type || string(e.Payload) != string(env.Payload) {
			t.Fatalf("unexpected event: %+v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func testIdempotency(t *testing.T, newStore Factory) {
	log, consumer, _, cleanup := newStore()
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	env := eventlog.EventEnvelope{
		PartitionKey:   "p2",
		IdempotencyKey: "idem1",
		Type:           "t1",
		Payload:        []byte("pay1"),
	}

	if err := log.Append(ctx, env); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(ctx, env); err != nil {
		t.Fatalf("idempotent append failed: %v", err)
	}

	var count int32
	received := make(chan struct{})

	go func() {
		_ = consumer.Run(ctx, func(c context.Context, e eventlog.Event) error {
			if e.PartitionKey == "p2" {
				atomic.AddInt32(&count, 1)
				received <- struct{}{}
			}
			return nil
		})
	}()

	select {
	case <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
	}

	select {
	case <-received:
		t.Fatal("event delivered multiple times despite idempotency key")
	case <-time.After(500 * time.Millisecond):
	}
	if v := atomic.LoadInt32(&count); v != 1 {
		t.Fatalf("expected 1 delivery, got %d", v)
	}
}

// testIdempotentFirstWinsPayload asserts that a re-append with the same
// IdempotencyKey but a different Payload does not replace the first payload.
func testIdempotentFirstWinsPayload(t *testing.T, newStore Factory) {
	log, consumer, _, cleanup := newStore()
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	partition := "p-first-wins"
	first := eventlog.EventEnvelope{
		PartitionKey:   partition,
		IdempotencyKey: "fw1",
		Type:           "t",
		Payload:        []byte("first-payload"),
	}
	retry := eventlog.EventEnvelope{
		PartitionKey:   partition,
		IdempotencyKey: "fw1",
		Type:           "t-other",
		Payload:        []byte("second-payload"),
	}
	if err := log.Append(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(ctx, retry); err != nil {
		t.Fatalf("retry append: %v", err)
	}

	done := make(chan eventlog.Event, 1)
	go func() {
		_ = consumer.Run(ctx, func(c context.Context, e eventlog.Event) error {
			if e.PartitionKey == partition {
				select {
				case done <- e:
				default:
				}
			}
			return nil
		})
	}()

	select {
	case e := <-done:
		if string(e.Payload) != "first-payload" {
			t.Fatalf("payload = %q, want first-wins %q", e.Payload, "first-payload")
		}
		if e.Type != "t" {
			t.Fatalf("type = %q, want first-wins %q", e.Type, "t")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for first-wins event")
	}

	select {
	case e := <-done:
		t.Fatalf("unexpected extra delivery after first-wins: %+v", e)
	case <-time.After(400 * time.Millisecond):
	}
}

func testPartitionKeyOrdering(t *testing.T, newStore Factory) {
	log, consumer, _, cleanup := newStore()
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	partition := "p-order"
	envelopes := []eventlog.EventEnvelope{
		{PartitionKey: partition, IdempotencyKey: "seq1", Type: "t", Payload: []byte("1")},
		{PartitionKey: partition, IdempotencyKey: "seq2", Type: "t", Payload: []byte("2")},
		{PartitionKey: partition, IdempotencyKey: "seq3", Type: "t", Payload: []byte("3")},
	}

	for _, e := range envelopes {
		if err := log.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	var received []string
	done := make(chan struct{})

	go func() {
		_ = consumer.Run(ctx, func(c context.Context, e eventlog.Event) error {
			if e.PartitionKey == partition {
				mu.Lock()
				received = append(received, string(e.Payload))
				if len(received) == 3 {
					close(done)
				}
				mu.Unlock()
			}
			return nil
		})
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for ordered events")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 3 {
		t.Fatalf("expected 3 events, got %d", len(received))
	}
	for i, want := range []string{"1", "2", "3"} {
		if received[i] != want {
			t.Errorf("at index %d, want %q, got %q", i, want, received[i])
		}
	}
}

func testAtLeastOnceRedelivery(t *testing.T, newStore Factory) {
	log, consumer, _, cleanup := newStore()
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	partition := "p-redeliver"
	env := eventlog.EventEnvelope{
		PartitionKey:   partition,
		IdempotencyKey: "err1",
		Type:           "t",
	}
	if err := log.Append(ctx, env); err != nil {
		t.Fatal(err)
	}

	var attempts int32
	delivered := make(chan struct{}, 2)

	go func() {
		_ = consumer.Run(ctx, func(c context.Context, e eventlog.Event) error {
			if e.PartitionKey == partition {
				n := atomic.AddInt32(&attempts, 1)
				select {
				case delivered <- struct{}{}:
				default:
				}
				if n == 1 {
					return errors.New("simulated temporary failure")
				}
			}
			return nil
		})
	}()

	select {
	case <-delivered:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for first delivery")
	}

	select {
	case <-delivered:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for redelivery")
	}

	select {
	case <-delivered:
		t.Fatal("event delivered a third time after success")
	case <-time.After(500 * time.Millisecond):
	}

	if v := atomic.LoadInt32(&attempts); v != 2 {
		t.Fatalf("expected 2 attempts, got %d", v)
	}
}

func testPositionTokenIntegrity(t *testing.T, newStore Factory) {
	log, consumer, _, cleanup := newStore()
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	partition := "p-token"
	env := eventlog.EventEnvelope{
		PartitionKey:   partition,
		IdempotencyKey: "pos1",
		Type:           "t",
	}
	if err := log.Append(ctx, env); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var tokenStr string
	var pKey string

	go func() {
		_ = consumer.Run(ctx, func(c context.Context, e eventlog.Event) error {
			if e.PartitionKey == partition {
				tokenStr = e.Position.Token()
				pKey = e.Position.PartitionKey()
				close(done)
			}
			return nil
		})
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
	}

	if pKey != partition {
		t.Errorf("Position.PartitionKey() = %q, want %q", pKey, partition)
	}
	if tokenStr == "" {
		t.Errorf("Position.Token() returned empty string, expected an opaque token")
	}

	pos := eventlog.NewPosition(pKey, tokenStr)
	if pos.PartitionKey() != pKey || pos.Token() != tokenStr {
		t.Errorf("NewPosition round-trip failed")
	}
}

// testMultiWorkerPartitionIsolation runs two consumers in the same group on a
// shared backend and asserts every event is processed exactly once across
// partitions (no double-processing).
func testMultiWorkerPartitionIsolation(t *testing.T, newShared SharedConsumersFactory) {
	log, consumerA, consumerB, _, cleanup := newShared()
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	partitions := []string{"mw-a", "mw-b"}
	var allKeys []string
	for _, p := range partitions {
		for i := 1; i <= 3; i++ {
			key := fmt.Sprintf("%s-e%d", p, i)
			allKeys = append(allKeys, key)
			env := eventlog.EventEnvelope{
				PartitionKey:   p,
				IdempotencyKey: key,
				Type:           "t",
				Payload:        []byte(key),
			}
			if err := log.Append(ctx, env); err != nil {
				t.Fatal(err)
			}
		}
	}

	var (
		mu   sync.Mutex
		seen = map[string]int{}
	)

	handle := func(_ context.Context, e eventlog.Event) error {
		if e.PartitionKey != "mw-a" && e.PartitionKey != "mw-b" {
			return nil
		}
		mu.Lock()
		seen[e.IdempotencyKey]++
		mu.Unlock()
		// Give the peer worker a chance to claim the other partition.
		time.Sleep(5 * time.Millisecond)
		return nil
	}

	go func() { _ = consumerA.Run(ctx, handle) }()
	go func() { _ = consumerB.Run(ctx, handle) }()

	deadline := time.After(10 * time.Second)
	for {
		mu.Lock()
		complete := true
		for _, k := range allKeys {
			if seen[k] < 1 {
				complete = false
				break
			}
		}
		mu.Unlock()
		if complete {
			break
		}
		select {
		case <-deadline:
			mu.Lock()
			t.Fatalf("timeout waiting for multi-worker processing; seen=%v", seen)
			mu.Unlock()
		case <-time.After(20 * time.Millisecond):
		}
	}

	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	for _, k := range allKeys {
		if n := seen[k]; n != 1 {
			t.Errorf("idempotency key %q processed %d times, want 1", k, n)
		}
	}
}

func testEventBusPublishSubscribe(t *testing.T, newStore Factory) {
	_, _, bus, cleanup := newStore()
	defer cleanup()
	if bus == nil {
		t.Skip("no EventBus")
	}

	ctx := context.Background()
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	notes, err := bus.Subscribe(subCtx, "cop.updated")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	want := []byte(`{"revision":7}`)
	if err := bus.Publish(ctx, "cop.updated", want); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case got, ok := <-notes:
		if !ok {
			t.Fatal("notes closed before receive")
		}
		if string(got) != string(want) {
			t.Fatalf("note = %q, want %q", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for bus note")
	}
}

func testEventBusCancelClosesChannel(t *testing.T, newStore Factory) {
	_, _, bus, cleanup := newStore()
	defer cleanup()
	if bus == nil {
		t.Skip("no EventBus")
	}

	subCtx, cancel := context.WithCancel(context.Background())
	notes, err := bus.Subscribe(subCtx, "cop.updated")
	if err != nil {
		cancel()
		t.Fatalf("Subscribe: %v", err)
	}
	cancel()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-notes:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("notes channel did not close after cancel")
		}
	}
}

func testEventBusTopicsIsolated(t *testing.T, newStore Factory) {
	_, _, bus, cleanup := newStore()
	defer cleanup()
	if bus == nil {
		t.Skip("no EventBus")
	}

	ctx := context.Background()
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
	time.Sleep(50 * time.Millisecond)

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
}

func testEventBusBackpressureDoesNotHang(t *testing.T, newStore Factory) {
	_, _, bus, cleanup := newStore()
	defer cleanup()
	if bus == nil {
		t.Skip("no EventBus")
	}

	ctx := context.Background()
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	notes, err := bus.Subscribe(subCtx, "cop.updated")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

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

	cancel()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-notes:
			if !ok {
				return
			}
		case <-deadline:
			return
		}
	}
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
