package pgstore

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

func TestEventConsumerDeliversPerPartitionOrder(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	c := NewEventConsumer(s, ConsumerConfig{
		ConsumerGroup: "test-order",
		IdleInterval:  10 * time.Millisecond,
		ErrorBackoff:  -1,
	})

	// Interleave two partitions so global sequence is not per-key dense.
	envelopes := []eventlog.EventEnvelope{
		{PartitionKey: "p-a", IdempotencyKey: "a1", Type: "t", Payload: []byte("a1")},
		{PartitionKey: "p-b", IdempotencyKey: "b1", Type: "t", Payload: []byte("b1")},
		{PartitionKey: "p-a", IdempotencyKey: "a2", Type: "t", Payload: []byte("a2")},
		{PartitionKey: "p-b", IdempotencyKey: "b2", Type: "t", Payload: []byte("b2")},
		{PartitionKey: "p-a", IdempotencyKey: "a3", Type: "t", Payload: []byte("a3")},
	}
	for _, env := range envelopes {
		if err := s.Append(ctx, env); err != nil {
			t.Fatalf("append %q: %v", env.IdempotencyKey, err)
		}
	}

	type delivery struct {
		partition string
		idemp     string
		sequence  uint64
	}
	var (
		mu     sync.Mutex
		got    []delivery
		byPart = map[string][]string{}
	)
	allDone := make(chan struct{})
	var once sync.Once

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Run(runCtx, func(_ context.Context, e eventlog.Event) error {
			mu.Lock()
			got = append(got, delivery{partition: e.PartitionKey, idemp: e.IdempotencyKey, sequence: e.Sequence})
			byPart[e.PartitionKey] = append(byPart[e.PartitionKey], e.IdempotencyKey)
			n := len(got)
			mu.Unlock()

			if e.Position.PartitionKey() != e.PartitionKey {
				return fmt.Errorf("position partition %q != event %q", e.Position.PartitionKey(), e.PartitionKey)
			}
			if e.Position.Token() != EncodeSequenceToken(int64(e.Sequence)) {
				return fmt.Errorf("position token %q != sequence %d", e.Position.Token(), e.Sequence)
			}
			if e.Timestamp.IsZero() {
				return errors.New("timestamp is zero")
			}
			if n >= len(envelopes) {
				once.Do(func() { close(allDone) })
			}
			return nil
		})
	}()

	waitConsumer(t, allDone, errCh, cancel, 10*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != len(envelopes) {
		t.Fatalf("delivered %d events, want %d: %#v", len(got), len(envelopes), got)
	}
	if want := []string{"a1", "a2", "a3"}; !stringSliceEqual(byPart["p-a"], want) {
		t.Fatalf("partition p-a order = %v, want %v", byPart["p-a"], want)
	}
	if want := []string{"b1", "b2"}; !stringSliceEqual(byPart["p-b"], want) {
		t.Fatalf("partition p-b order = %v, want %v", byPart["p-b"], want)
	}
	// Within each partition, sequences must be strictly increasing.
	var lastA, lastB uint64
	for _, d := range got {
		switch d.partition {
		case "p-a":
			if lastA != 0 && d.sequence <= lastA {
				t.Fatalf("p-a sequence not ordered: prev=%d curr=%d", lastA, d.sequence)
			}
			lastA = d.sequence
		case "p-b":
			if lastB != 0 && d.sequence <= lastB {
				t.Fatalf("p-b sequence not ordered: prev=%d curr=%d", lastB, d.sequence)
			}
			lastB = d.sequence
		}
	}
}

func TestEventConsumerMultiWorkerNoDoubleProcess(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	const (
		group      = "test-multi-worker"
		partitions = 4
		perPart    = 5
	)
	total := partitions * perPart
	for p := 0; p < partitions; p++ {
		for n := 0; n < perPart; n++ {
			env := eventlog.EventEnvelope{
				PartitionKey:   fmt.Sprintf("part-%d", p),
				IdempotencyKey: fmt.Sprintf("part-%d-evt-%d", p, n),
				Type:           "t",
				Payload:        []byte(fmt.Sprintf("%d:%d", p, n)),
			}
			if err := s.Append(ctx, env); err != nil {
				t.Fatalf("append: %v", err)
			}
		}
	}

	var (
		mu        sync.Mutex
		seen      = map[string]int{}
		partOrder = map[string][]string{}
		doneCount atomic.Int64
	)
	allDone := make(chan struct{})
	var once sync.Once

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	handle := func(_ context.Context, e eventlog.Event) error {
		// Small stall so both workers overlap on lock acquisition.
		time.Sleep(2 * time.Millisecond)
		mu.Lock()
		seen[e.IdempotencyKey]++
		partOrder[e.PartitionKey] = append(partOrder[e.PartitionKey], e.IdempotencyKey)
		mu.Unlock()
		if doneCount.Add(1) >= int64(total) {
			once.Do(func() { close(allDone) })
		}
		return nil
	}

	cfg := ConsumerConfig{ConsumerGroup: group, IdleInterval: 5 * time.Millisecond, ErrorBackoff: -1}
	c1 := NewEventConsumer(s, cfg)
	c2 := NewEventConsumer(s, cfg)

	errCh := make(chan error, 2)
	go func() { errCh <- c1.Run(runCtx, handle) }()
	go func() { errCh <- c2.Run(runCtx, handle) }()

	select {
	case <-allDone:
		// Allow in-flight commits to finish before cancelling Run contexts.
		time.Sleep(50 * time.Millisecond)
		cancel()
	case <-time.After(15 * time.Second):
		cancel()
		t.Fatal("timeout waiting for multi-worker drain")
	}

	deadline := time.After(5 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("Run worker: %v", err)
			}
		case <-deadline:
			t.Fatal("timeout waiting for workers to exit")
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != total {
		t.Fatalf("unique deliveries = %d, want %d", len(seen), total)
	}
	for key, count := range seen {
		if count != 1 {
			t.Fatalf("event %q delivered %d times, want 1 (double-process)", key, count)
		}
	}
	for p := 0; p < partitions; p++ {
		key := fmt.Sprintf("part-%d", p)
		var want []string
		for n := 0; n < perPart; n++ {
			want = append(want, fmt.Sprintf("part-%d-evt-%d", p, n))
		}
		if !stringSliceEqual(partOrder[key], want) {
			t.Fatalf("partition %s order = %v, want %v", key, partOrder[key], want)
		}
	}
}

func TestEventConsumerHandlerErrorRedeliversWithoutAdvancing(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	c := NewEventConsumer(s, ConsumerConfig{
		ConsumerGroup: "test-redeliver",
		IdleInterval:  5 * time.Millisecond,
		ErrorBackoff:  5 * time.Millisecond,
	})

	if err := s.Append(ctx, eventlog.EventEnvelope{
		PartitionKey: "p1", IdempotencyKey: "only", Type: "t", Payload: []byte("x"),
	}); err != nil {
		t.Fatal(err)
	}

	var attempts atomic.Int64
	successSeen := make(chan struct{})
	var once sync.Once

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Run(runCtx, func(_ context.Context, e eventlog.Event) error {
			n := attempts.Add(1)
			if n < 3 {
				// Checkpoint must not advance: prove via later redelivery.
				token := readCheckpointToken(t, s, "test-redeliver", "p1")
				if token != "" {
					return fmt.Errorf("checkpoint advanced after failed attempt: %q", token)
				}
				return errors.New("simulated handler failure")
			}
			if e.IdempotencyKey != "only" {
				return fmt.Errorf("unexpected event %q", e.IdempotencyKey)
			}
			once.Do(func() { close(successSeen) })
			return nil
		})
	}()

	waitConsumer(t, successSeen, errCh, cancel, 10*time.Second)

	if got := attempts.Load(); got < 3 {
		t.Fatalf("handler attempts = %d, want >= 3", got)
	}

	token := readCheckpointToken(t, s, "test-redeliver", "p1")
	if token == "" {
		t.Fatal("checkpoint missing after successful redelivery ack")
	}
	seq, err := DecodeSequenceToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if seq < 1 {
		t.Fatalf("checkpoint sequence = %d", seq)
	}
}

func TestEventConsumerSuccessCheckpointSkipsOnRestart(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	group := "test-restart"
	cfg := ConsumerConfig{ConsumerGroup: group, IdleInterval: 5 * time.Millisecond, ErrorBackoff: -1}

	for _, key := range []string{"e1", "e2", "e3"} {
		if err := s.Append(ctx, eventlog.EventEnvelope{
			PartitionKey: "p1", IdempotencyKey: key, Type: "t", Payload: []byte(key),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// First Run: consume all three.
	c1 := NewEventConsumer(s, cfg)
	runCtx1, cancel1 := context.WithCancel(ctx)
	defer cancel1()
	var first []string
	var firstMu sync.Mutex
	firstDone := make(chan struct{})
	var firstOnce sync.Once
	errCh := make(chan error, 1)
	go func() {
		errCh <- c1.Run(runCtx1, func(_ context.Context, e eventlog.Event) error {
			firstMu.Lock()
			first = append(first, e.IdempotencyKey)
			n := len(first)
			firstMu.Unlock()
			if n >= 3 {
				firstOnce.Do(func() { close(firstDone) })
			}
			return nil
		})
	}()
	waitConsumer(t, firstDone, errCh, cancel1, 10*time.Second)

	firstMu.Lock()
	if !stringSliceEqual(first, []string{"e1", "e2", "e3"}) {
		firstMu.Unlock()
		t.Fatalf("first deliveries = %v", first)
	}
	firstMu.Unlock()

	// Second Run: append one more event; only the new one should be delivered.
	if err := s.Append(ctx, eventlog.EventEnvelope{
		PartitionKey: "p1", IdempotencyKey: "e4", Type: "t", Payload: []byte("e4"),
	}); err != nil {
		t.Fatal(err)
	}

	c2 := NewEventConsumer(s, cfg)
	runCtx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()
	var second []string
	var secondMu sync.Mutex
	secondDone := make(chan struct{})
	var secondOnce sync.Once
	errCh2 := make(chan error, 1)
	go func() {
		errCh2 <- c2.Run(runCtx2, func(_ context.Context, e eventlog.Event) error {
			secondMu.Lock()
			second = append(second, e.IdempotencyKey)
			secondMu.Unlock()
			secondOnce.Do(func() { close(secondDone) })
			return nil
		})
	}()
	waitConsumer(t, secondDone, errCh2, cancel2, 10*time.Second)

	secondMu.Lock()
	defer secondMu.Unlock()
	if !stringSliceEqual(second, []string{"e4"}) {
		t.Fatalf("restart deliveries = %v, want [e4] (acknowledged events redelivered)", second)
	}
}

func TestEventConsumerAtomicityHandlerErrorRollsBackSideEffectAndCheckpoint(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	group := "test-atomic"

	// Side-effect table written only through the ambient transaction.
	if _, err := s.Pool().Exec(ctx, `
		CREATE TABLE consumer_side_effects (
			idemp TEXT PRIMARY KEY,
			payload TEXT NOT NULL
		)`); err != nil {
		t.Fatalf("create side effect table: %v", err)
	}

	if err := s.Append(ctx, eventlog.EventEnvelope{
		PartitionKey: "p1", IdempotencyKey: "atomic-1", Type: "t", Payload: []byte("body"),
	}); err != nil {
		t.Fatal(err)
	}

	c := NewEventConsumer(s, ConsumerConfig{
		ConsumerGroup: group,
		IdleInterval:  5 * time.Millisecond,
		ErrorBackoff:  5 * time.Millisecond,
	})

	var phase atomic.Int64
	successSeen := make(chan struct{})
	var once sync.Once

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Run(runCtx, func(txCtx context.Context, e eventlog.Event) error {
			// Write a side effect using the TX-scoped executor path.
			if err := s.WithinTransaction(txCtx, func(inner context.Context) error {
				exec, err := s.executor(inner)
				if err != nil {
					return err
				}
				_, err = exec.Exec(inner, `
					INSERT INTO consumer_side_effects (idemp, payload) VALUES ($1, $2)
					ON CONFLICT (idemp) DO UPDATE SET payload = EXCLUDED.payload`,
					e.IdempotencyKey, string(e.Payload))
				return err
			}); err != nil {
				return err
			}

			n := phase.Add(1)
			if n == 1 {
				// After the insert, still no durable rows (same uncommitted TX).
				var midCount int
				if err := s.Pool().QueryRow(context.Background(),
					`SELECT COUNT(*) FROM consumer_side_effects`).Scan(&midCount); err != nil {
					return err
				}
				if midCount != 0 {
					return fmt.Errorf("side effect visible before commit: count=%d", midCount)
				}
				// Fail after partial work in the same TX — outer consumer TX
				// must roll back both the side effect and the checkpoint.
				return errors.New("fail after side effect")
			}
			once.Do(func() { close(successSeen) })
			return nil
		})
	}()

	waitConsumer(t, successSeen, errCh, cancel, 10*time.Second)

	if phase.Load() < 2 {
		t.Fatalf("expected redelivery after failed attempt, phase=%d", phase.Load())
	}

	// Final success should leave exactly one side-effect row and a checkpoint.
	var count int
	if err := s.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM consumer_side_effects`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("side effect rows = %d, want 1 (failed attempt must not commit)", count)
	}
	token := readCheckpointToken(t, s, group, "p1")
	if token == "" {
		t.Fatal("checkpoint not advanced after successful attempt")
	}
}

func TestEventConsumerCompileTimeInterface(t *testing.T) {
	// Runtime mirror of the package-level var _ eventlog.EventConsumer assertion.
	var _ eventlog.EventConsumer = (*EventConsumer)(nil)
	s := newTestStore(t)
	c := NewEventConsumer(s, ConsumerConfig{})
	if c.ConsumerGroup() != DefaultConsumerGroup {
		t.Fatalf("default group = %q, want %q", c.ConsumerGroup(), DefaultConsumerGroup)
	}
}

// waitConsumer waits until done is closed (handler success path), then cancels
// Run after a short grace so the project+position commit can finish. Cancel is
// never invoked from inside handle, which would race the TX commit on ctx.
func waitConsumer(t *testing.T, done <-chan struct{}, errCh <-chan error, cancel context.CancelFunc, timeout time.Duration) {
	t.Helper()
	select {
	case <-done:
		time.Sleep(50 * time.Millisecond)
		cancel()
	case err := <-errCh:
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run exited early: %v", err)
		}
		t.Fatal("Run exited before work completed")
	case <-time.After(timeout):
		cancel()
		t.Fatal("timeout waiting for consumer work")
	}
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Run to exit after cancel")
	}
}

func readCheckpointToken(t *testing.T, s *Store, group, partition string) string {
	t.Helper()
	var token string
	err := s.Pool().QueryRow(context.Background(), `
		SELECT position_token FROM event_consumer_checkpoints
		WHERE consumer_group = $1 AND partition_key = $2`, group, partition,
	).Scan(&token)
	if err != nil {
		return ""
	}
	return token
}

func stringSliceEqual(a, b []string) bool {
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
