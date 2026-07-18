package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/replay"
	"mosaic.local/mosaic/internal/state"
	"mosaic.local/mosaic/internal/store"
)

type expectedScenario struct {
	CanonicalEvents []gen.CanonicalEvent `json:"canonical_events"`
}

func TestDomesticScenarioProjectionIsDeterministic(t *testing.T) {
	ctx := context.Background()
	events := domesticEvents(t)
	projected := projectScenario(t, ctx, events, len(events))

	if projected.StateRevision != 9 || projected.Checkpoint.ThroughCanonicalSeq != 9 {
		t.Fatalf("revision/checkpoint = %d/%d, want 9/9", projected.StateRevision, projected.Checkpoint.ThroughCanonicalSeq)
	}
	assertContains(t, stringsAt(projected.COP, "effective_event_ids"), "canonical-domestic-009-road-open")
	assertNotContains(t, stringsAt(projected.COP, "effective_event_ids"), "canonical-domestic-007-repaired-road")
	assertStateField(t, objectsAt(projected.COP, "roads"), "road_id", "road-brook-lane", "status", "open")
	assertStateField(t, objectsAt(projected.COP, "resources"), "resource_id", "resource-ems-004", "availability", "unavailable")
	assertStateField(t, objectsAt(projected.COP, "units"), "unit_id", "unit-017", "availability", "assigned")
	assertStateField(t, objectsAt(projected.COP, "weather_alerts"), "weather_alert_id", "weather-heavy-rain-001", "status", "active")
	assertStateField(t, objectsAt(projected.COP, "incidents"), "incident_id", "incident-domestic-001", "status", "open")

	again := projectScenario(t, ctx, events, len(events))
	firstJSON := marshalCOP(t, projected.COP)
	secondJSON := marshalCOP(t, again.COP)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("identical canonical log produced different COP bytes:\n%s\n%s", firstJSON, secondJSON)
	}
}

func TestCheckpointRestartReplaysLateDeliveryAndCorrection(t *testing.T) {
	ctx := context.Background()
	events := domesticEvents(t)
	s, projector := newStoreProjector(t, ctx)
	defer s.Close()
	appendEvents(t, ctx, s, events)

	for _, event := range events[:7] {
		if _, err := projector.ApplyCanonicalEvent(ctx, event); err != nil {
			t.Fatalf("apply %s: %v", event.CanonicalEventID, err)
		}
	}
	checkpoint, err := s.LatestCheckpoint(ctx)
	if err != nil {
		t.Fatalf("latest checkpoint: %v", err)
	}
	if checkpoint.ThroughCanonicalSeq != 7 {
		t.Fatalf("checkpoint sequence = %d, want 7", checkpoint.ThroughCanonicalSeq)
	}

	recoveredProjector, err := state.NewProjector(s, s, s)
	if err != nil {
		t.Fatalf("new recovery projector: %v", err)
	}
	recovered, err := (replay.Runner{Canonical: s, Checkpoints: s, Projector: recoveredProjector}).Recover(ctx)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	fresh, err := recoveredProjector.Replay(ctx, gen.Checkpoint{}, events)
	if err != nil {
		t.Fatalf("fresh replay: %v", err)
	}
	if recovered.StateRevision != 9 {
		t.Fatalf("recovered revision = %d, want 9", recovered.StateRevision)
	}
	if string(marshalCOP(t, recovered.COP)) != string(marshalCOP(t, fresh.COP)) {
		t.Fatal("checkpoint plus later events did not rebuild the exact fresh COP")
	}
	assertStateField(t, objectsAt(recovered.COP, "resources"), "resource_id", "resource-ems-004", "availability", "unavailable")
	assertStateField(t, objectsAt(recovered.COP, "roads"), "road_id", "road-brook-lane", "status", "open")
}

func TestRetryDoesNotCreateSecondRevisionOrCheckpoint(t *testing.T) {
	ctx := context.Background()
	events := domesticEvents(t)
	s, projector := newStoreProjector(t, ctx)
	defer s.Close()
	appendEvents(t, ctx, s, events[:1])

	first, err := projector.ApplyCanonicalEvent(ctx, events[0])
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	second, err := projector.ApplyCanonicalEvent(ctx, events[0])
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if first.StateRevision != 1 || second.StateRevision != 1 {
		t.Fatalf("revisions = %d and %d, want 1", first.StateRevision, second.StateRevision)
	}
	assertRowCount(t, ctx, s, "checkpoints", 1)
	assertRowCount(t, ctx, s, "canonical_projection_receipts", 1)
}

func TestApplyRequiresCanonicalAppendOrder(t *testing.T) {
	ctx := context.Background()
	events := domesticEvents(t)
	s, projector := newStoreProjector(t, ctx)
	defer s.Close()
	appendEvents(t, ctx, s, events[:2])

	_, err := projector.ApplyCanonicalEvent(ctx, events[1])
	if !errors.Is(err, state.ErrProjectionOrder) {
		t.Fatalf("error = %v, want ErrProjectionOrder", err)
	}
	assertRowCount(t, ctx, s, "checkpoints", 0)
	assertRowCount(t, ctx, s, "canonical_projection_receipts", 0)
}
func TestCheckpointFailureRollsBackProjectionReceipt(t *testing.T) {
	ctx := context.Background()
	events := domesticEvents(t)
	s, err := store.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	appendEvents(t, ctx, s, events[:1])
	projector, err := state.NewProjector(s, failingCheckpoints{CheckpointRepository: s}, s)
	if err != nil {
		t.Fatalf("new projector: %v", err)
	}
	if _, err := projector.ApplyCanonicalEvent(ctx, events[0]); err == nil {
		t.Fatal("apply succeeded despite checkpoint failure")
	}
	assertRowCount(t, ctx, s, "checkpoints", 0)
	assertRowCount(t, ctx, s, "canonical_projection_receipts", 0)
}

func TestUnknownEventHasExplicitDeterministicTreatment(t *testing.T) {
	ctx := context.Background()
	projector, err := state.NewProjector(noopCanonical{}, noopCheckpoints{}, noopTransactions{})
	if err != nil {
		t.Fatalf("new projector: %v", err)
	}
	_, err = projector.Replay(ctx, gen.Checkpoint{}, []gen.CanonicalEvent{{
		CanonicalEventID: "unknown-1",
		CanonicalSeq:     1,
		EventType:        "unknown_event",
		ReceivedAt:       "2026-07-18T10:00:00Z",
	}})
	if !errors.Is(err, state.ErrUnsupportedEventType) {
		t.Fatalf("error = %v, want ErrUnsupportedEventType", err)
	}
}

func newStoreProjector(t *testing.T, ctx context.Context) (*store.Store, *state.Projector) {
	t.Helper()
	s, err := store.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	projector, err := state.NewProjector(s, s, s)
	if err != nil {
		_ = s.Close()
		t.Fatalf("new projector: %v", err)
	}
	return s, projector
}

func projectScenario(t *testing.T, ctx context.Context, events []gen.CanonicalEvent, applyCount int) contracts.ProjectionResult {
	t.Helper()
	s, projector := newStoreProjector(t, ctx)
	defer s.Close()
	appendEvents(t, ctx, s, events)
	var result contracts.ProjectionResult
	for _, event := range events[:applyCount] {
		var err error
		result, err = projector.ApplyCanonicalEvent(ctx, event)
		if err != nil {
			t.Fatalf("apply %s: %v", event.CanonicalEventID, err)
		}
	}
	return result
}

func appendEvents(t *testing.T, ctx context.Context, s *store.Store, events []gen.CanonicalEvent) {
	t.Helper()
	for _, event := range events {
		if _, err := s.AppendRawEvent(ctx, gen.RawEvent{
			SchemaVersion: "1.0.0",
			RawEventID:    event.RawEventID,
			Source: map[string]any{
				"source_id":        "state-test",
				"source_record_id": event.RawEventID,
			},
			ContentType:     "application/json",
			PayloadBytesB64: "e30=",
			RawSha256:       "test",
			ReceivedAt:      event.ReceivedAt,
		}); err != nil {
			t.Fatalf("append raw %s: %v", event.RawEventID, err)
		}
		stored, err := s.AppendCanonicalEvent(ctx, event)
		if err != nil {
			t.Fatalf("append canonical %s: %v", event.CanonicalEventID, err)
		}
		if stored.CanonicalSeq != event.CanonicalSeq {
			t.Fatalf("stored sequence for %s = %d, want %d", event.CanonicalEventID, stored.CanonicalSeq, event.CanonicalSeq)
		}
	}
}

func domesticEvents(t *testing.T) []gen.CanonicalEvent {
	t.Helper()
	bytes, err := os.ReadFile(filepath.Join("..", "..", "datasets", "domestic-disturbance", "expected-outcomes.json"))
	if err != nil {
		t.Fatalf("read expected scenario: %v", err)
	}
	var scenario expectedScenario
	if err := json.Unmarshal(bytes, &scenario); err != nil {
		t.Fatalf("decode expected scenario: %v", err)
	}
	sort.Slice(scenario.CanonicalEvents, func(i, j int) bool {
		return scenario.CanonicalEvents[i].CanonicalSeq < scenario.CanonicalEvents[j].CanonicalSeq
	})
	return scenario.CanonicalEvents
}

func marshalCOP(t *testing.T, value map[string]any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal COP: %v", err)
	}
	return encoded
}

func stringsAt(cop map[string]any, key string) []string {
	values, _ := cop[key].([]any)
	output := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			output = append(output, text)
		}
	}
	return output
}

func objectsAt(cop map[string]any, key string) []map[string]any {
	values, _ := cop[key].([]any)
	output := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if object, ok := value.(map[string]any); ok {
			output = append(output, object)
		}
	}
	return output
}

func assertStateField(t *testing.T, values []map[string]any, idKey, id, field, want string) {
	t.Helper()
	for _, value := range values {
		if value[idKey] == id {
			if got, _ := value[field].(string); got != want {
				t.Fatalf("%s %q %s = %q, want %q", idKey, id, field, got, want)
			}
			return
		}
	}
	t.Fatalf("no %s %q in COP", idKey, id)
}

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("%q not found in %v", want, values)
}

func assertNotContains(t *testing.T, values []string, unwanted string) {
	t.Helper()
	for _, value := range values {
		if value == unwanted {
			t.Fatalf("unexpected %q found in %v", unwanted, values)
		}
	}
}

func assertRowCount(t *testing.T, ctx context.Context, s *store.Store, table string, want int) {
	t.Helper()
	var count int
	if err := s.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != want {
		t.Fatalf("%s rows = %d, want %d", table, count, want)
	}
}

type failingCheckpoints struct{ contracts.CheckpointRepository }

func (failingCheckpoints) AppendCheckpoint(context.Context, gen.Checkpoint) error {
	return errors.New("forced checkpoint failure")
}

type noopCanonical struct{}

func (noopCanonical) AppendCanonicalEvent(context.Context, gen.CanonicalEvent) (gen.CanonicalEvent, error) {
	return gen.CanonicalEvent{}, errors.New("unused")
}
func (noopCanonical) ListCanonicalEventsAfter(context.Context, int64) ([]gen.CanonicalEvent, error) {
	return nil, nil
}
func (noopCanonical) ListEffectiveCanonicalEventsForIncident(context.Context, string) ([]gen.CanonicalEvent, error) {
	return nil, nil
}
func (noopCanonical) MarkCanonicalEventProjected(context.Context, string, int64) error {
	return errors.New("unused")
}

type noopCheckpoints struct{}

func (noopCheckpoints) AppendCheckpoint(context.Context, gen.Checkpoint) error {
	return errors.New("unused")
}
func (noopCheckpoints) LatestCheckpoint(context.Context) (gen.Checkpoint, error) {
	return gen.Checkpoint{}, errors.New("unused")
}

type noopTransactions struct{}

func (noopTransactions) WithinTransaction(_ context.Context, fn func(context.Context) error) error {
	return fn(context.Background())
}
