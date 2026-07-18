package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
)

func TestOpenAppliesMigrationsAndRejectsHistoryMutation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	tables := []string{
		"raw_events", "canonical_events", "luna_results", "insights", "recommendations",
		"model_runs", "audit_records", "checkpoints", "canonical_projection_receipts",
	}
	for _, table := range tables {
		var name string
		err := s.SQLDB().QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
		if err != nil {
			t.Fatalf("migration did not create %s: %v", table, err)
		}
	}

	if _, err := s.AppendRawEvent(ctx, rawEvent("raw-immutable", "dispatch", "source-immutable", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SQLDB().ExecContext(ctx, "UPDATE raw_events SET source_id = 'changed' WHERE raw_event_id = 'raw-immutable'"); err == nil {
		t.Fatal("append-only trigger allowed UPDATE")
	}
	if _, err := s.SQLDB().ExecContext(ctx, "DELETE FROM raw_events WHERE raw_event_id = 'raw-immutable'"); err == nil {
		t.Fatal("append-only trigger allowed DELETE")
	}
}

func TestRawEventIdempotencyReturnsExistingLifecycleResult(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	first := rawEvent("raw-1", "dispatch", "record-1", "")

	appended, err := s.AppendRawEvent(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if !appended.Created || appended.RawEventID != first.RawEventID {
		t.Fatalf("first append = %#v, want a created raw-1", appended)
	}
	if err := s.AppendLunaResult(ctx, gen.LunaResult{LunaResultID: "luna-1", RawEventID: first.RawEventID}); err != nil {
		t.Fatal(err)
	}

	duplicate, err := s.AppendRawEvent(ctx, rawEvent("raw-duplicate", "dispatch", "record-1", ""))
	if err != nil {
		t.Fatal(err)
	}
	want := contracts.RawEventAppendResult{RawEventID: "raw-1", ExistingResultID: "luna-1"}
	if duplicate != want {
		t.Fatalf("duplicate append = %#v, want %#v", duplicate, want)
	}
	if _, err := s.FindRawEvent(ctx, "raw-duplicate"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("duplicate created another source record: %v", err)
	}
}

func TestCanonicalSequenceIsDatabaseOrdered(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	appendRaw(t, s, rawEvent("raw-1", "dispatch", "record-1", ""))
	appendRaw(t, s, rawEvent("raw-2", "dispatch", "record-2", ""))

	first, err := s.AppendCanonicalEvent(ctx, canonicalEvent("event-1", "raw-1", "incident-1", ""))
	if err != nil {
		t.Fatal(err)
	}
	secondInput := canonicalEvent("event-2", "raw-2", "incident-1", "")
	secondInput.CanonicalSeq = 9000
	second, err := s.AppendCanonicalEvent(ctx, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if first.CanonicalSeq != 1 || second.CanonicalSeq != 2 {
		t.Fatalf("canonical sequences = %d, %d; want 1, 2", first.CanonicalSeq, second.CanonicalSeq)
	}

	events, err := s.ListCanonicalEventsAfter(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].CanonicalEventID != "event-1" || events[1].CanonicalEventID != "event-2" {
		t.Fatalf("canonical append order = %#v", events)
	}
	if events[1].CanonicalSeq != 2 {
		t.Fatalf("stored canonical sequence = %d, want 2", events[1].CanonicalSeq)
	}
}

func TestEffectiveEventsChooseHighestDirectCorrection(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	for i := 1; i <= 4; i++ {
		appendRaw(t, s, rawEvent(fmt.Sprintf("raw-%d", i), "dispatch", fmt.Sprintf("record-%d", i), ""))
	}
	appendCanonical(t, s, canonicalEvent("original", "raw-1", "incident-1", ""))
	appendCanonical(t, s, canonicalEvent("unrelated", "raw-2", "incident-1", ""))
	appendCanonical(t, s, canonicalEvent("lower-correction", "raw-3", "incident-1", "original"))
	appendCanonical(t, s, canonicalEvent("higher-correction", "raw-4", "incident-1", "original"))

	effective, err := s.ListEffectiveCanonicalEventsForIncident(ctx, "incident-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(effective) != 2 {
		t.Fatalf("effective events = %#v, want unrelated plus highest correction", effective)
	}
	if effective[0].CanonicalEventID != "unrelated" || effective[1].CanonicalEventID != "higher-correction" {
		t.Fatalf("effective event IDs = %q, %q; want unrelated, higher-correction", effective[0].CanonicalEventID, effective[1].CanonicalEventID)
	}
}

func TestTransactionRollbackLeavesNoDurableRecord(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	wantErr := errors.New("abort test transaction")
	err := s.WithinTransaction(ctx, func(txCtx context.Context) error {
		if _, err := s.AppendRawEvent(txCtx, rawEvent("raw-rollback", "dispatch", "record-rollback", "")); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("transaction error = %v, want %v", err, wantErr)
	}
	if _, err := s.FindRawEvent(ctx, "raw-rollback"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled-back event was persisted: %v", err)
	}
}

func TestLatestCheckpointUsesHighestStateRevision(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.AppendCheckpoint(ctx, checkpoint("checkpoint-1", 1, 3)); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendCheckpoint(ctx, checkpoint("checkpoint-2", 2, 8)); err != nil {
		t.Fatal(err)
	}

	latest, err := s.LatestCheckpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if latest.CheckpointID != "checkpoint-2" || latest.StateRevision != 2 || latest.ThroughCanonicalSeq != 8 {
		t.Fatalf("latest checkpoint = %#v", latest)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenInMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close test store: %v", err)
		}
	})
	return s
}

func rawEvent(id, sourceID, sourceRecordID, idempotencyKey string) gen.RawEvent {
	source := map[string]any{"source_id": sourceID}
	if sourceRecordID != "" {
		source["source_record_id"] = sourceRecordID
	}
	if idempotencyKey != "" {
		source["idempotency_key"] = idempotencyKey
	}
	return gen.RawEvent{
		RawEventID: id,
		Source:     source,
	}
}

func canonicalEvent(id, rawEventID, incidentID, supersedesEventID string) gen.CanonicalEvent {
	return gen.CanonicalEvent{
		CanonicalEventID:  id,
		RawEventID:        rawEventID,
		IncidentRefs:      []any{incidentID},
		SupersedesEventID: supersedesEventID,
	}
}

func checkpoint(id string, stateRevision, throughCanonicalSeq int64) gen.Checkpoint {
	return gen.Checkpoint{
		CheckpointID:        id,
		StateRevision:       stateRevision,
		ThroughCanonicalSeq: throughCanonicalSeq,
		COP:                 []byte(`{"state_revision":1}`),
	}
}

func appendRaw(t *testing.T, s *Store, event gen.RawEvent) {
	t.Helper()
	if _, err := s.AppendRawEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
}

func appendCanonical(t *testing.T, s *Store, event gen.CanonicalEvent) gen.CanonicalEvent {
	t.Helper()
	appended, err := s.AppendCanonicalEvent(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	return appended
}
