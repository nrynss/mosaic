package eventlogtest

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/eventlog"
)

// RunConformanceTests validates a transport backend against the EventLog and
// EventConsumer contracts. It guarantees the backend correctly implements
// append idempotency, partition ordering, at-least-once delivery, and position
// cursor integrity.
func RunConformanceTests(t *testing.T, newStore func() (eventlog.EventLog, eventlog.EventConsumer, func())) {
	t.Run("AppendAndConsume", func(t *testing.T) { testAppendAndConsume(t, newStore) })
	t.Run("Idempotency", func(t *testing.T) { testIdempotency(t, newStore) })
	t.Run("PartitionKeyOrdering", func(t *testing.T) { testPartitionKeyOrdering(t, newStore) })
	t.Run("AtLeastOnceRedelivery", func(t *testing.T) { testAtLeastOnceRedelivery(t, newStore) })
	t.Run("PositionTokenIntegrity", func(t *testing.T) { testPositionTokenIntegrity(t, newStore) })
}

func testAppendAndConsume(t *testing.T, newStore func() (eventlog.EventLog, eventlog.EventConsumer, func())) {
	log, consumer, cleanup := newStore()
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

func testIdempotency(t *testing.T, newStore func() (eventlog.EventLog, eventlog.EventConsumer, func())) {
	log, consumer, cleanup := newStore()
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

func testPartitionKeyOrdering(t *testing.T, newStore func() (eventlog.EventLog, eventlog.EventConsumer, func())) {
	log, consumer, cleanup := newStore()
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

func testAtLeastOnceRedelivery(t *testing.T, newStore func() (eventlog.EventLog, eventlog.EventConsumer, func())) {
	log, consumer, cleanup := newStore()
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

func testPositionTokenIntegrity(t *testing.T, newStore func() (eventlog.EventLog, eventlog.EventConsumer, func())) {
	log, consumer, cleanup := newStore()
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
