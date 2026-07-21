package memory_test

import (
	"context"
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
}

func TestAppendRequiresKeys(t *testing.T) {
	log := memory.New()
	err := log.Append(context.Background(), eventlog.EventEnvelope{
		IdempotencyKey: "x",
		Type:           "raw.event",
	})
	if err == nil {
		t.Fatal("expected PartitionKey required")
	}
}
