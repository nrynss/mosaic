package simulator

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"mosaic.local/mosaic/internal/store"
)

func TestDomesticDisturbanceRunsDeterministicSpine(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestService(t, ctx)

	result, err := service.Run(ctx)
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	if result.ScenarioID != "scenario-domestic-disturbance-v1" || result.StateRevision != 9 {
		t.Fatalf("scenario/revision = %q/%d, want domestic scenario/9", result.ScenarioID, result.StateRevision)
	}
	if len(result.Timeline) != 10 {
		t.Fatalf("timeline entries = %d, want 10", len(result.Timeline))
	}
	assertStep(t, result, "raw-domestic-001-call", "accepted", 1)
	assertStep(t, result, "raw-domestic-007-incomplete-road", "repaired", 7)
	assertStep(t, result, "raw-domestic-008-invalid-input", "quarantined", 7)
	assertStep(t, result, "raw-domestic-009-late-ems", "accepted", 8)
	assertStep(t, result, "raw-domestic-010-road-correction", "accepted", 9)
	if !roadStatus(result.COP, "road-brook-lane", "open") {
		t.Fatal("final COP does not contain the repaired-road opening correction")
	}
	if !resourceAvailability(result.COP, "resource-ems-004", "unavailable") {
		t.Fatal("late EMS delivery did not update the final COP")
	}
	if service.NormalizerCalls() != 10 {
		t.Fatalf("fixture Luna calls = %d, want 10", service.NormalizerCalls())
	}
	if !result.Verification.RawEventsRetained || !result.Verification.LunaLifecyclesMatch || !result.Verification.ModelRunProvenanceValid || !result.Verification.CanonicalTimelineMatch || !result.Verification.RoadCorrectionApplied || !result.Verification.TerraObsolescenceFixtureExpected || !result.Verification.CheckpointRecoveryMatches {
		t.Fatalf("verification = %#v", result.Verification)
	}
	if result.Verification.LateDeliverySequence != 8 {
		t.Fatalf("late delivery sequence = %d, want 8", result.Verification.LateDeliverySequence)
	}
}

func TestExactDuplicateDoesNotInvokeFixtureLuna(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestService(t, ctx)
	if _, err := service.Run(ctx); err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	before := service.NormalizerCalls()
	fixture, err := LoadFixture(filepath.Join("..", "..", "..", "..", "datasets", DomesticDisturbance))
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	duplicate, ok := fixture.RawEvent("raw-domestic-001-call")
	if !ok {
		t.Fatal("missing baseline raw fixture")
	}
	duplicate.RawEventID = "raw-domestic-001-call-duplicate-delivery"
	outcome, err := service.Ingest(ctx, duplicate)
	if err != nil {
		t.Fatalf("ingest exact duplicate: %v", err)
	}
	if !outcome.Duplicate || outcome.Status != "duplicate" || outcome.RawEventID != "raw-domestic-001-call" {
		t.Fatalf("duplicate outcome = %#v", outcome)
	}
	if service.NormalizerCalls() != before {
		t.Fatalf("fixture Luna calls after duplicate = %d, want %d", service.NormalizerCalls(), before)
	}
}

func TestCheckpointRecoveryMatchesFinalScenarioCOP(t *testing.T) {
	ctx := context.Background()
	service, database := newTestService(t, ctx)
	run, err := service.Run(ctx)
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	restarted, err := New(Config{
		Store:      database,
		SchemaDir:  filepath.Join("..", "..", "..", "..", "ontology"),
		FixtureDir: filepath.Join("..", "..", "..", "..", "datasets", DomesticDisturbance),
	})
	if err != nil {
		t.Fatalf("restart simulator: %v", err)
	}
	recovered, err := restarted.Recover(ctx)
	if err != nil {
		t.Fatalf("recover checkpoint: %v", err)
	}
	left, err := json.Marshal(run.COP)
	if err != nil {
		t.Fatalf("marshal run COP: %v", err)
	}
	right, err := json.Marshal(recovered.COP)
	if err != nil {
		t.Fatalf("marshal recovered COP: %v", err)
	}
	if string(left) != string(right) || recovered.StateRevision != run.StateRevision {
		t.Fatalf("checkpoint recovery differs: revision %d/%d\n%s\n%s", recovered.StateRevision, run.StateRevision, right, left)
	}
}

func newTestService(t *testing.T, ctx context.Context) (*Service, *store.Store) {
	t.Helper()
	database, err := store.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	service, err := New(Config{
		Store:      database,
		SchemaDir:  filepath.Join("..", "..", "..", "..", "ontology"),
		FixtureDir: filepath.Join("..", "..", "..", "..", "datasets", DomesticDisturbance),
	})
	if err != nil {
		t.Fatalf("new simulator: %v", err)
	}
	return service, database
}

func assertStep(t *testing.T, result RunResult, rawEventID, status string, revision int64) {
	t.Helper()
	for _, step := range result.Timeline {
		if step.RawEventID == rawEventID {
			if step.LifecycleStatus != status || step.StateRevision != revision {
				t.Fatalf("step %s = status %q revision %d, want %q/%d", rawEventID, step.LifecycleStatus, step.StateRevision, status, revision)
			}
			return
		}
	}
	t.Fatalf("timeline does not contain %s", rawEventID)
}

func resourceAvailability(cop map[string]any, resourceID, availability string) bool {
	resources, _ := cop["resources"].([]any)
	for _, value := range resources {
		resource, _ := value.(map[string]any)
		if resource["resource_id"] == resourceID && resource["availability"] == availability {
			return true
		}
	}
	return false
}

func TestServiceBeatsMatchScenario(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestService(t, ctx)

	beats := service.Beats()

	fixtureDir := filepath.Join("..", "..", "..", "..", "datasets", DomesticDisturbance)
	scenarioPath := filepath.Join(fixtureDir, "scenario.json")

	var scenario scenarioDocument
	if err := decodeFile(scenarioPath, &scenario); err != nil {
		t.Fatalf("failed to decode scenario.json: %v", err)
	}

	expectedBeats := scenario.Beats
	sort.Slice(expectedBeats, func(i, j int) bool { return expectedBeats[i].Order < expectedBeats[j].Order })

	if len(beats) != len(expectedBeats) {
		t.Fatalf("number of beats = %d, want %d", len(beats), len(expectedBeats))
	}

	for i, expected := range expectedBeats {
		actual := beats[i]
		if actual.BeatID != expected.BeatID {
			t.Errorf("beat[%d] ID = %q, want %q", i, actual.BeatID, expected.BeatID)
		}
		if actual.Order != expected.Order {
			t.Errorf("beat[%d] Order = %d, want %d", i, actual.Order, expected.Order)
		}
		if actual.RawEventID != expected.RawEventID {
			t.Errorf("beat[%d] RawEventID = %q, want %q", i, actual.RawEventID, expected.RawEventID)
		}
		expectedDelay := time.Duration(expected.DelayMS) * time.Millisecond
		if actual.Delay != expectedDelay {
			t.Errorf("beat[%d] Delay = %v, want %v", i, actual.Delay, expectedDelay)
		}
	}
}
