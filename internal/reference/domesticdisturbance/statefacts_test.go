package domesticdisturbance

import (
	"context"
	"testing"
)

func TestResolveStateFactMatchesEffectiveEventAndCollections(t *testing.T) {
	ctx := context.Background()
	cop := map[string]any{
		"state_revision":      float64(9),
		"effective_event_ids": []any{"canonical-road-blocked-7"},
		"incidents":           []any{map[string]any{"incident_id": "incident-1", "category": "domestic_disturbance"}},
		"roads":               []any{map[string]any{"road_id": "road-1", "status": "blocked"}},
	}

	resolver := StateFacts{}

	effective, err := resolver.ResolveStateFact(ctx, "canonical-road-blocked-7", cop)
	if err != nil {
		t.Fatalf("resolve effective event: %v", err)
	}
	if !effective.Resolved {
		t.Fatalf("effective event fact did not resolve: %#v", effective)
	}
	if artifact, ok := effective.Artifact.(map[string]any); !ok || artifact["fact_kind"] != "effective_event" {
		t.Fatalf("effective event artifact = %#v", effective.Artifact)
	}

	incident, err := resolver.ResolveStateFact(ctx, "incident-1", cop)
	if err != nil {
		t.Fatalf("resolve incident: %v", err)
	}
	if !incident.Resolved {
		t.Fatalf("incident fact did not resolve: %#v", incident)
	}

	road, err := resolver.ResolveStateFact(ctx, "road-1", cop)
	if err != nil {
		t.Fatalf("resolve road: %v", err)
	}
	if !road.Resolved {
		t.Fatalf("road fact did not resolve: %#v", road)
	}
}

func TestResolveStateFactReportsMissingAndEmptyCOP(t *testing.T) {
	ctx := context.Background()
	resolver := StateFacts{}

	missing, err := resolver.ResolveStateFact(ctx, "unknown-1", map[string]any{"incidents": []any{}})
	if err != nil {
		t.Fatalf("resolve missing: %v", err)
	}
	if missing.Resolved {
		t.Fatalf("missing fact was presented as resolved: %#v", missing)
	}

	nilCOP, err := resolver.ResolveStateFact(ctx, "incident-1", nil)
	if err != nil {
		t.Fatalf("resolve nil COP: %v", err)
	}
	if nilCOP.Resolved {
		t.Fatalf("nil COP fact was presented as resolved: %#v", nilCOP)
	}
}
