package memory_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"mosaic.local/mosaic/internal/eventlog"
	"mosaic.local/mosaic/internal/eventlog/memory"
)

func TestAppendIdempotent(t *testing.T) {
	log := memory.New()
	env := eventlog.EventEnvelope{
		PartitionKey:   "domestic-disturbance",
		IdempotencyKey: "raw-1",
		Type:           "raw.event",
		Payload:        []byte(`{"raw_event_id":"raw-1"}`),
	}
	if err := log.Append(context.Background(), env); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := log.Append(context.Background(), env); err != nil {
		t.Fatalf("duplicate append: %v", err)
	}
	if log.Len() != 1 {
		t.Fatalf("len = %d, want 1", log.Len())
	}
	events := log.Events()
	if len(events) != 1 {
		t.Fatalf("Events len = %d, want 1", len(events))
	}
	if events[0].IdempotencyKey != "raw-1" || events[0].Type != "raw.event" {
		t.Fatalf("unexpected stored event: %+v", events[0])
	}
}

func TestAppendSameIdempotencyDifferentPartitionFirstWins(t *testing.T) {
	log := memory.New()
	first := eventlog.EventEnvelope{
		PartitionKey:   "p-a",
		IdempotencyKey: "shared",
		Type:           "t-a",
		Payload:        []byte("a"),
	}
	second := eventlog.EventEnvelope{
		PartitionKey:   "p-b",
		IdempotencyKey: "shared",
		Type:           "t-b",
		Payload:        []byte("b"),
	}
	if err := log.Append(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if log.Len() != 1 {
		t.Fatalf("len = %d, want 1 (global idempotency)", log.Len())
	}
	e := log.Events()[0]
	if e.PartitionKey != "p-a" || e.Type != "t-a" || string(e.Payload) != "a" {
		t.Fatalf("first-wins lost: %+v", e)
	}
}

func TestAppendRequiresKeys(t *testing.T) {
	log := memory.New()
	cases := []struct {
		name string
		env  eventlog.EventEnvelope
	}{
		{
			name: "missing PartitionKey",
			env:  eventlog.EventEnvelope{IdempotencyKey: "x", Type: "raw.event"},
		},
		{
			name: "whitespace PartitionKey",
			env:  eventlog.EventEnvelope{PartitionKey: "  ", IdempotencyKey: "x", Type: "raw.event"},
		},
		{
			name: "missing IdempotencyKey",
			env:  eventlog.EventEnvelope{PartitionKey: "p", Type: "raw.event"},
		},
		{
			name: "whitespace IdempotencyKey",
			env:  eventlog.EventEnvelope{PartitionKey: "p", IdempotencyKey: "\t", Type: "raw.event"},
		},
		{
			name: "missing Type",
			env:  eventlog.EventEnvelope{PartitionKey: "p", IdempotencyKey: "x"},
		},
		{
			name: "whitespace Type",
			env:  eventlog.EventEnvelope{PartitionKey: "p", IdempotencyKey: "x", Type: "  "},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := log.Append(context.Background(), tc.env); err == nil {
				t.Fatal("expected validation error")
			}
			if log.Len() != 0 {
				t.Fatalf("len = %d after rejected append", log.Len())
			}
		})
	}
}

func TestAppendNilPayload(t *testing.T) {
	log := memory.New()
	env := eventlog.EventEnvelope{
		PartitionKey:   "p",
		IdempotencyKey: "nil-payload",
		Type:           "t",
		Payload:        nil,
	}
	if err := log.Append(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	events := log.Events()
	if len(events) != 1 {
		t.Fatalf("len = %d, want 1", len(events))
	}
	if events[0].Payload == nil {
		t.Fatal("stored payload should be non-nil empty slice")
	}
	if len(events[0].Payload) != 0 {
		t.Fatalf("payload len = %d, want 0", len(events[0].Payload))
	}
}

func TestAppendPayloadIsolation(t *testing.T) {
	log := memory.New()
	payload := []byte("original")
	env := eventlog.EventEnvelope{
		PartitionKey:   "p",
		IdempotencyKey: "iso-1",
		Type:           "t",
		Payload:        payload,
	}
	if err := log.Append(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	payload[0] = 'X'
	// Mutate slice returned by Events as well.
	got := log.Events()
	if string(got[0].Payload) != "original" {
		t.Fatalf("stored payload mutated via input slice: %q", got[0].Payload)
	}
	got[0].Payload[0] = 'Y'
	again := log.Events()
	if string(again[0].Payload) != "original" {
		t.Fatalf("stored payload mutated via Events copy: %q", again[0].Payload)
	}
}

func TestConcurrentAppendDistinctKeys(t *testing.T) {
	log := memory.New()
	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("k-%d", i)
			env := eventlog.EventEnvelope{
				PartitionKey:   "p-concurrent",
				IdempotencyKey: key,
				Type:           "t",
				Payload:        []byte(key),
			}
			errs <- log.Append(context.Background(), env)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if log.Len() != n {
		t.Fatalf("len = %d, want %d", log.Len(), n)
	}
	// Sequences within the partition should be unique and cover 1..n.
	seenSeq := map[uint64]bool{}
	for _, e := range log.Events() {
		if e.PartitionKey != "p-concurrent" {
			t.Fatalf("unexpected partition %q", e.PartitionKey)
		}
		if seenSeq[e.Sequence] {
			t.Fatalf("duplicate sequence %d", e.Sequence)
		}
		seenSeq[e.Sequence] = true
	}
	if len(seenSeq) != n {
		t.Fatalf("unique sequences = %d, want %d", len(seenSeq), n)
	}
}

func TestAppendCancelledContext(t *testing.T) {
	log := memory.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := log.Append(ctx, eventlog.EventEnvelope{
		PartitionKey:   "p",
		IdempotencyKey: "k",
		Type:           "t",
	})
	if err == nil {
		t.Fatal("expected context error")
	}
}
