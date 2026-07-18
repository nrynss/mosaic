package ingestion_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ingestion"
	"mosaic.local/mosaic/internal/luna"
	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/store"
)

var fixedTime = time.Date(2026, time.July, 18, 10, 0, 0, 0, time.UTC)

type fakeLuna struct {
	output contracts.LunaOutput
	err    error
	calls  int
}

func (f *fakeLuna) Normalize(context.Context, gen.RawEvent) (contracts.LunaOutput, error) {
	f.calls++
	return f.output, f.err
}

type fakeDispatcher struct {
	ids []string
	err error
}

func (f *fakeDispatcher) DispatchCanonicalEvent(_ context.Context, canonicalEventID string) error {
	f.ids = append(f.ids, canonicalEventID)
	return f.err
}

type harness struct {
	store      *store.Store
	service    *ingestion.Service
	normalizer *fakeLuna
	dispatcher *fakeDispatcher
}

func newHarness(t *testing.T, output contracts.LunaOutput, normalizeErr, dispatchErr error) harness {
	t.Helper()
	ctx := context.Background()
	database, err := store.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open in-memory store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	validator, err := luna.LoadSchemaValidator(filepath.Join("..", "..", "ontology"))
	if err != nil {
		t.Fatalf("load schema validator: %v", err)
	}
	normalizer := &fakeLuna{output: output, err: normalizeErr}
	dispatcher := &fakeDispatcher{err: dispatchErr}
	service, err := ingestion.New(ingestion.Config{
		RawEvents:       database,
		CanonicalEvents: database,
		Records:         database,
		Transactions:    database,
		Luna:            normalizer,
		Dispatcher:      dispatcher,
		Validator:       validator,
		Clock:           func() time.Time { return fixedTime },
	})
	if err != nil {
		t.Fatalf("create ingestion service: %v", err)
	}
	return harness{store: database, service: service, normalizer: normalizer, dispatcher: dispatcher}
}

func TestIngestPersistsRawBeforeLunaFailure(t *testing.T) {
	raw := testRaw("raw-failure", "source-failure")
	// The envelope is valid, but the decoded application/json source body is
	// deliberately malformed. Ingestion must retain it before Luna fails.
	raw.ContentType = "application/json"
	raw.PayloadBytesB64 = "eyBtYWxmb3JtZWQgaW5jaWRlbnQgcGF5bG9hZA=="
	failure := failedOutput(raw, "failure")
	h := newHarness(t, failure, errors.New("adapter unavailable"), nil)

	outcome, err := h.service.Ingest(context.Background(), raw)
	if !errors.Is(err, ingestion.ErrLunaNormalization) {
		t.Fatalf("Ingest error = %v, want Luna normalization error", err)
	}
	if outcome.Status != "rejected" || outcome.RawEventID != raw.RawEventID || outcome.LunaResultID != failure.Result.LunaResultID {
		t.Fatalf("failure outcome = %#v", outcome)
	}
	if h.normalizer.calls != 1 {
		t.Fatalf("Luna calls = %d, want 1", h.normalizer.calls)
	}
	stored, err := h.store.FindRawEvent(context.Background(), raw.RawEventID)
	if err != nil || stored.PayloadBytesB64 != raw.PayloadBytesB64 {
		t.Fatalf("raw envelope was not retained: stored=%#v err=%v", stored, err)
	}
	assertCount(t, h.store, "model_runs", 1)
	assertCount(t, h.store, "luna_results", 1)
	assertCount(t, h.store, "canonical_events", 0)
}

func TestIngestDuplicateDoesNotNormalizeOrRedispatch(t *testing.T) {
	raw := testRaw("raw-original", "source-duplicate")
	h := newHarness(t, acceptedOutput(raw, "original", "accepted"), nil, nil)
	first, err := h.service.Ingest(context.Background(), raw)
	if err != nil {
		t.Fatalf("first Ingest: %v", err)
	}
	duplicate := testRaw("raw-duplicate-delivery", "source-duplicate")
	second, err := h.service.Ingest(context.Background(), duplicate)
	if err != nil {
		t.Fatalf("duplicate Ingest: %v", err)
	}
	if !second.Duplicate || second.RawEventID != first.RawEventID || second.LunaResultID != first.LunaResultID || second.Status != "duplicate" {
		t.Fatalf("duplicate outcome = %#v, first = %#v", second, first)
	}
	if h.normalizer.calls != 1 || len(h.dispatcher.ids) != 1 {
		t.Fatalf("duplicate calls: Luna=%d dispatch=%v", h.normalizer.calls, h.dispatcher.ids)
	}
	assertCount(t, h.store, "raw_events", 1)
	assertCount(t, h.store, "model_runs", 1)
	assertCount(t, h.store, "luna_results", 1)
	assertCount(t, h.store, "canonical_events", 1)
}

func TestIngestAcceptedAndRepairedAppendCanonicalLifecycle(t *testing.T) {
	for _, status := range []string{"accepted", "repaired"} {
		t.Run(status, func(t *testing.T) {
			raw := testRaw("raw-"+status, "source-"+status)
			output := acceptedOutput(raw, status, status)
			// Luna does not assign the durable replay sequence; P03 does.
			output.CanonicalEvent.CanonicalSeq = 0
			h := newHarness(t, output, nil, nil)

			outcome, err := h.service.Ingest(context.Background(), raw)
			if err != nil {
				t.Fatalf("Ingest: %v", err)
			}
			if outcome.Status != status || outcome.CanonicalEventID != output.CanonicalEvent.CanonicalEventID {
				t.Fatalf("outcome = %#v", outcome)
			}
			events, err := h.store.ListCanonicalEventsAfter(context.Background(), 0)
			if err != nil {
				t.Fatalf("list canonical events: %v", err)
			}
			if len(events) != 1 || events[0].CanonicalSeq != 1 || events[0].RawEventID != raw.RawEventID {
				t.Fatalf("canonical events = %#v", events)
			}
			assertCount(t, h.store, "model_runs", 1)
			assertCount(t, h.store, "luna_results", 1)
			assertCount(t, h.store, "canonical_events", 1)
			if got := h.dispatcher.ids; len(got) != 1 || got[0] != outcome.CanonicalEventID {
				t.Fatalf("dispatches = %v", got)
			}
		})
	}
}

func TestIngestQuarantinedAndInvalidOutputDoNotAppendCanonical(t *testing.T) {
	t.Run("quarantined", func(t *testing.T) {
		raw := testRaw("raw-quarantined", "source-quarantined")
		h := newHarness(t, quarantinedOutput(raw, "quarantined"), nil, nil)
		outcome, err := h.service.Ingest(context.Background(), raw)
		if err != nil {
			t.Fatalf("Ingest: %v", err)
		}
		if outcome.Status != "quarantined" || outcome.CanonicalEventID != "" {
			t.Fatalf("quarantined outcome = %#v", outcome)
		}
		assertCount(t, h.store, "raw_events", 1)
		assertCount(t, h.store, "model_runs", 1)
		assertCount(t, h.store, "luna_results", 1)
		assertCount(t, h.store, "canonical_events", 0)
		if len(h.dispatcher.ids) != 0 {
			t.Fatalf("quarantined dispatches = %v", h.dispatcher.ids)
		}
	})

	t.Run("invalid canonical relationship", func(t *testing.T) {
		raw := testRaw("raw-invalid", "source-invalid")
		output := acceptedOutput(raw, "invalid", "accepted")
		output.CanonicalEvent.RawEventID = "another-raw-event"
		output.ModelRun.ValidationStatus = "invalid"
		h := newHarness(t, output, nil, nil)

		outcome, err := h.service.Ingest(context.Background(), raw)
		if !errors.Is(err, ingestion.ErrInvalidLunaOutput) {
			t.Fatalf("Ingest error = %v, want invalid Luna output", err)
		}
		if outcome.Status != "rejected" || outcome.CanonicalEventID != "" {
			t.Fatalf("invalid outcome = %#v", outcome)
		}
		assertCount(t, h.store, "raw_events", 1)
		assertCount(t, h.store, "model_runs", 1)
		assertCount(t, h.store, "luna_results", 1)
		assertCount(t, h.store, "canonical_events", 0)
	})
}

func TestIngestCanonicalAndLunaResultRollbackTogether(t *testing.T) {
	firstRaw := testRaw("raw-atomic-first", "source-atomic-first")
	first := acceptedOutput(firstRaw, "atomic-first", "accepted")
	first.Result.LunaResultID = "shared-luna-result"
	first.ModelRun.OutputIds = []any{first.Result.LunaResultID, first.CanonicalEvent.CanonicalEventID}
	h := newHarness(t, first, nil, nil)
	if _, err := h.service.Ingest(context.Background(), firstRaw); err != nil {
		t.Fatalf("first Ingest: %v", err)
	}

	secondRaw := testRaw("raw-atomic-second", "source-atomic-second")
	second := acceptedOutput(secondRaw, "atomic-second", "accepted")
	second.Result.LunaResultID = "shared-luna-result"
	second.ModelRun.OutputIds = []any{second.Result.LunaResultID, second.CanonicalEvent.CanonicalEventID}
	h.normalizer.output = second

	if _, err := h.service.Ingest(context.Background(), secondRaw); err == nil {
		t.Fatal("second Ingest succeeded, want lifecycle transaction failure")
	}
	// The raw envelope and model-run remain durable, but the failed Luna Result
	// insertion rolls back the canonical append in the same transaction.
	assertCount(t, h.store, "raw_events", 2)
	assertCount(t, h.store, "model_runs", 2)
	assertCount(t, h.store, "luna_results", 1)
	assertCount(t, h.store, "canonical_events", 1)
}

func TestIngestDispatchesOnlyAfterCommitAndExposesFailure(t *testing.T) {
	raw := testRaw("raw-dispatch", "source-dispatch")
	dispatchErr := errors.New("projector unavailable")
	h := newHarness(t, acceptedOutput(raw, "dispatch", "accepted"), nil, dispatchErr)

	outcome, err := h.service.Ingest(context.Background(), raw)
	if err != nil {
		t.Fatalf("Ingest returned persistence error after commit: %v", err)
	}
	if !errors.Is(outcome.DispatchError, dispatchErr) {
		t.Fatalf("DispatchError = %v, want %v", outcome.DispatchError, dispatchErr)
	}
	assertCount(t, h.store, "canonical_events", 1)
	assertCount(t, h.store, "luna_results", 1)
	if got := h.dispatcher.ids; len(got) != 1 || got[0] != outcome.CanonicalEventID {
		t.Fatalf("dispatches = %v, outcome = %#v", got, outcome)
	}
}

func TestIngestSemanticDuplicatePreservesBothCanonicalObservations(t *testing.T) {
	firstRaw := testRaw("raw-semantic-first", "source-semantic-first")
	firstOutput := acceptedOutput(firstRaw, "semantic-first", "accepted")
	h := newHarness(t, firstOutput, nil, nil)
	first, err := h.service.Ingest(context.Background(), firstRaw)
	if err != nil {
		t.Fatalf("first Ingest: %v", err)
	}

	secondRaw := testRaw("raw-semantic-second", "source-semantic-second")
	secondOutput := acceptedOutput(secondRaw, "semantic-second", "accepted")
	secondOutput.CanonicalEvent.DuplicateOf = first.CanonicalEventID
	h.normalizer.output = secondOutput
	second, err := h.service.Ingest(context.Background(), secondRaw)
	if err != nil {
		t.Fatalf("semantic duplicate Ingest: %v", err)
	}
	if second.Duplicate {
		t.Fatal("semantic duplicate was treated as idempotent delivery")
	}
	events, err := h.store.ListCanonicalEventsAfter(context.Background(), 0)
	if err != nil {
		t.Fatalf("list canonical events: %v", err)
	}
	if len(events) != 2 || events[1].DuplicateOf != first.CanonicalEventID || events[1].RawEventID != secondRaw.RawEventID {
		t.Fatalf("semantic duplicate history = %#v", events)
	}
}

func testRaw(rawEventID, sourceRecordID string) gen.RawEvent {
	return gen.RawEvent{
		SchemaVersion:   "1.0.0",
		RawEventID:      rawEventID,
		Source:          map[string]any{"source_id": "synthetic-test", "source_record_id": sourceRecordID},
		ContentType:     "text/plain",
		PayloadBytesB64: "eA==",
		RawSha256:       strings.Repeat("a", 64),
		ReceivedAt:      fixedTime.Format(time.RFC3339Nano),
	}
}

func acceptedOutput(raw gen.RawEvent, suffix, status string) contracts.LunaOutput {
	canonicalID := "canonical-" + suffix
	resultID := "luna-result-" + suffix
	runID := "luna-run-" + suffix
	canonical := gen.CanonicalEvent{
		SchemaVersion:    "1.0.0",
		CanonicalEventID: canonicalID,
		CanonicalSeq:     1,
		RawEventID:       raw.RawEventID,
		EventType:        "road_status_changed",
		OccurredAt:       fixedTime.Format(time.RFC3339Nano),
		ReceivedAt:       fixedTime.Add(time.Second).Format(time.RFC3339Nano),
		Payload:          map[string]any{"road_id": "road-1", "status": "blocked"},
		EntityRefs:       []any{map[string]any{"kind": "road", "id": "road-1"}},
		Provenance: json.RawMessage(fmt.Sprintf(
			`{"normalizer":"test","raw_event_id":%q,"model_run_id":%q}`,
			raw.RawEventID, runID,
		)),
		Confidence: json.RawMessage(`{"source_quality":"high","transformation_certainty":"high","reasoning_support":"low","basis":"synthetic test"}`),
	}
	result := gen.LunaResult{
		SchemaVersion:    "1.0.0",
		LunaResultID:     resultID,
		RawEventID:       raw.RawEventID,
		Status:           status,
		CanonicalEventID: canonicalID,
		Evidence:         []any{evidence(raw.RawEventID)},
		CreatedAt:        fixedTime.Add(2 * time.Second).Format(time.RFC3339Nano),
	}
	if status == "repaired" {
		result.Repair = json.RawMessage(`{"method":"synthetic repair","fields":[{"json_pointer":"/payload/road_id","original":"","replacement":"road-1"}]}`)
	}
	return contracts.LunaOutput{
		Result:         result,
		CanonicalEvent: &canonical,
		ModelRun: gen.ModelRun{
			SchemaVersion:       "1.0.0",
			ModelRunID:          runID,
			Agent:               "luna",
			Provider:            "test",
			Model:               "test-model",
			PromptVersion:       "v1",
			OutputSchemaVersion: "1.0.0",
			InputEventIds:       []any{raw.RawEventID},
			OutputIds:           []any{resultID, canonicalID},
			ValidationStatus:    "valid",
			StartedAt:           fixedTime.Format(time.RFC3339Nano),
			CompletedAt:         fixedTime.Add(time.Second).Format(time.RFC3339Nano),
		},
	}
}

func quarantinedOutput(raw gen.RawEvent, suffix string) contracts.LunaOutput {
	resultID := "luna-result-" + suffix
	return contracts.LunaOutput{
		Result: gen.LunaResult{
			SchemaVersion: "1.0.0",
			LunaResultID:  resultID,
			RawEventID:    raw.RawEventID,
			Status:        "quarantined",
			Reason:        "source payload requires review",
			CreatedAt:     fixedTime.Add(time.Second).Format(time.RFC3339Nano),
		},
		ModelRun: gen.ModelRun{
			SchemaVersion:       "1.0.0",
			ModelRunID:          "luna-run-" + suffix,
			Agent:               "luna",
			Provider:            "test",
			Model:               "test-model",
			PromptVersion:       "v1",
			OutputSchemaVersion: "1.0.0",
			InputEventIds:       []any{raw.RawEventID},
			OutputIds:           []any{resultID},
			ValidationStatus:    "valid",
			StartedAt:           fixedTime.Format(time.RFC3339Nano),
			CompletedAt:         fixedTime.Add(time.Second).Format(time.RFC3339Nano),
		},
	}
}

func failedOutput(raw gen.RawEvent, suffix string) contracts.LunaOutput {
	output := quarantinedOutput(raw, suffix)
	output.Result.Status = "rejected"
	output.Result.Reason = "provider timeout"
	output.ModelRun.ValidationStatus = "timed_out"
	output.ModelRun.FailureDetail = "provider timeout"
	return output
}

func evidence(rawEventID string) map[string]any {
	return map[string]any{
		"target_kind": "raw_event",
		"target_id":   rawEventID,
		"explanation": "synthetic source envelope",
	}
}

func assertCount(t *testing.T, database *store.Store, table string, want int) {
	t.Helper()
	allowed := map[string]bool{
		"raw_events":       true,
		"canonical_events": true,
		"luna_results":     true,
		"model_runs":       true,
	}
	if !allowed[table] {
		t.Fatalf("unapproved test table %q", table)
	}
	var got int
	if err := database.SQLDB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}
