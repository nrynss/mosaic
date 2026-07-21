package democast

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mosaic.local/mosaic/internal/openaimodel"
	"mosaic.local/mosaic/internal/sol"
	"mosaic.local/mosaic/internal/terra"
)

// EchoTerra is an offline Terra StructuredClient that returns a schema-valid
// Insight whose evidence exactly matches the request (sameEvidence green).
type EchoTerra struct {
	InsightID string
}

// Assess implements terra.StructuredClient.
func (c EchoTerra) Assess(_ context.Context, req terra.Request) (terra.Response, error) {
	id := strings.TrimSpace(c.InsightID)
	if id == "" {
		id = "insight-demo-cassette-terra-001"
	}
	evidence := make([]any, 0, len(req.Evidence))
	for _, item := range req.Evidence {
		ref := map[string]any{
			"target_kind": item.TargetKind,
			"target_id":   item.TargetID,
			"explanation": item.Explanation,
		}
		if p := strings.TrimSpace(item.JsonPointer); p != "" {
			ref["json_pointer"] = p
		}
		evidence = append(evidence, ref)
	}
	insight := map[string]any{
		"schema_version":   "1.0.0",
		"insight_id":       id,
		"state_revision":   req.StateRevision,
		"lifecycle_status": "active",
		"assertions":       []string{"Demo cassette Terra assessment of access and road constraints."},
		"evidence":         evidence,
		"confidence": map[string]any{
			"source_quality":           "medium",
			"transformation_certainty": "medium",
			"reasoning_support":        "high",
			"basis":                    "Offline stub echoes permitted evidence for cassette identity proof.",
		},
		"created_at": "2026-07-18T12:00:00Z",
	}
	encoded, err := json.Marshal(insight)
	if err != nil {
		return terra.Response{}, fmt.Errorf("marshal stub insight: %w", err)
	}
	return terra.Response{
		InsightJSON: encoded,
		ResponseID:  "stub-terra-demo-cassette",
	}, nil
}

// EchoSol is an offline Sol StructuredClient that returns a schema-valid
// Recommendation whose evidence exactly matches the request.
type EchoSol struct {
	RecommendationID string
}

// Brief implements sol.StructuredClient.
func (c EchoSol) Brief(_ context.Context, req sol.Request) (sol.Response, error) {
	id := strings.TrimSpace(c.RecommendationID)
	if id == "" {
		id = "recommendation-demo-cassette-sol-001"
	}
	evidence := make([]any, 0, len(req.Evidence))
	for _, item := range req.Evidence {
		ref := map[string]any{
			"target_kind": item.TargetKind,
			"target_id":   item.TargetID,
			"explanation": item.Explanation,
		}
		if p := strings.TrimSpace(item.JsonPointer); p != "" {
			ref["json_pointer"] = p
		}
		evidence = append(evidence, ref)
	}
	rec := map[string]any{
		"schema_version":    "1.0.0",
		"recommendation_id": id,
		"state_revision":    req.StateRevision,
		"text":              "Consider reviewing the access constraint narrative with the supervisor before deciding.",
		"evidence":          evidence,
		"created_at":        "2026-07-18T12:00:05Z",
	}
	encoded, err := json.Marshal(rec)
	if err != nil {
		return sol.Response{}, fmt.Errorf("marshal stub recommendation: %w", err)
	}
	return sol.Response{
		RecommendationJSON: encoded,
		ResponseID:         "stub-sol-demo-cassette",
	}, nil
}

// StubLuna is an offline LunaStructuredClient that returns schema-shaped
// LunaResult JSON. Quarantine set mirrors the live bank outcomes captured in
// recording-manifest expected_status so offline identity proof stays aligned
// with CI-strict AssertOperatorOK.
type StubLuna struct{}

// stubLunaQuarantineIDs matches the live OpenAI bank (see demo cassette runbook).
// Only the intentionally bare invalid-input beat stays quarantined after
// attribute enrichment; request-derived cassette keys are independent of status.
var stubLunaQuarantineIDs = map[string]string{
	"raw-domestic-008-invalid-input": "Offline stub: intentionally malformed input fixture.",
}

// Normalize implements openaimodel.LunaStructuredClient.
func (StubLuna) Normalize(_ context.Context, req openaimodel.LunaRequest) (openaimodel.LunaResponse, error) {
	rawID := extractRawEventID(req.RawEventJSON)
	if rawID == "" {
		rawID = "unknown"
	}
	if reason, quarantine := stubLunaQuarantineIDs[rawID]; quarantine {
		result := map[string]any{
			"schema_version": "1.0.0",
			"luna_result_id": "luna-demo-cassette-quarantine-" + rawID,
			"raw_event_id":   rawID,
			"status":         "quarantined",
			"reason":         reason,
			"created_at":     "2026-07-18T12:00:00Z",
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return openaimodel.LunaResponse{}, err
		}
		return openaimodel.LunaResponse{
			ResultJSON: encoded,
			ResponseID: "stub-luna-quarantine-" + rawID,
		}, nil
	}
	result := map[string]any{
		"schema_version":     "1.0.0",
		"luna_result_id":     "luna-demo-cassette-" + rawID,
		"raw_event_id":       rawID,
		"status":             "accepted",
		"canonical_event_id": "canonical-demo-cassette-" + rawID,
		"evidence": []any{
			map[string]any{
				"target_kind": "raw_event",
				"target_id":   rawID,
				"explanation": "Offline stub accepts the synthetic envelope.",
			},
		},
		"created_at": "2026-07-18T12:00:00Z",
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return openaimodel.LunaResponse{}, err
	}
	return openaimodel.LunaResponse{
		ResultJSON: encoded,
		ResponseID: "stub-luna-" + rawID,
	}, nil
}

func extractRawEventID(raw json.RawMessage) string {
	var envelope struct {
		RawEventID string `json:"raw_event_id"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ""
	}
	return strings.TrimSpace(envelope.RawEventID)
}
