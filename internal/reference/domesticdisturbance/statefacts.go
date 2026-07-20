package domesticdisturbance

import (
	"context"

	"mosaic.local/mosaic/internal/api"
)

// StateFacts resolves state_fact evidence for the domestic-disturbance domain.
// The generic API owns stored-record resolution; this resolver owns the shape
// and identifiers of the domain's deterministic COP facts. It performs no I/O
// and only reads the COP snapshot supplied by the recovery seam.
type StateFacts struct{}

var _ api.StateFactResolver = StateFacts{}

// ResolveStateFact matches an evidence ID against the effective canonical
// events and the domestic COP collections. A missing fact is reported as an
// explicit unresolved result rather than an error.
func (StateFacts) ResolveStateFact(_ context.Context, id string, cop map[string]any) (api.Resolution, error) {
	resolution := api.Resolution{Kind: "state_fact", ID: id}
	if cop == nil {
		resolution.Reason = "no COP snapshot is available"
		return resolution, nil
	}
	for _, eventID := range stringsAt(cop["effective_event_ids"]) {
		if eventID == id {
			resolution.Resolved = true
			resolution.Artifact = map[string]any{
				"fact_kind":          "effective_event",
				"canonical_event_id": eventID,
				"state_revision":     cop["state_revision"],
			}
			return resolution, nil
		}
	}

	for _, candidate := range []struct {
		collection string
		idField    string
	}{
		{collection: "incidents", idField: "incident_id"},
		{collection: "units", idField: "unit_id"},
		{collection: "resources", idField: "resource_id"},
		{collection: "roads", idField: "road_id"},
		{collection: "weather_alerts", idField: "weather_alert_id"},
	} {
		for _, fact := range objectsAt(cop[candidate.collection]) {
			if factID, _ := fact[candidate.idField].(string); factID == id {
				resolution.Resolved = true
				resolution.Artifact = fact
				return resolution, nil
			}
		}
	}

	resolution.Reason = "state fact is not present in the current COP"
	return resolution, nil
}

func stringsAt(value any) []string {
	values, ok := value.([]any)
	if !ok {
		if asStrings, isStrings := value.([]string); isStrings {
			return asStrings
		}
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if stringValue, ok := value.(string); ok {
			result = append(result, stringValue)
		}
	}
	return result
}

func objectsAt(value any) []map[string]any {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if object, ok := value.(map[string]any); ok {
			result = append(result, object)
		}
	}
	return result
}
