package luna

import (
	"fmt"
	"time"

	"mosaic.local/mosaic/internal/ontology/gen"
)

// FailureArtifacts is the durable, schema-valid record created when the
// ingestion boundary cannot trust an adapter response. It preserves the Raw
// Event while explicitly recording that no Canonical Event was produced.
type FailureArtifacts struct {
	ModelRun gen.ModelRun
	Result   gen.LunaResult
}

// NewFailureArtifacts creates a Luna model-run and rejected lifecycle result
// for an adapter or response-validation failure. The raw event has already been
// persisted when this is called, so these deterministic IDs are safe from
// collision within the idempotent ingestion lifecycle.
func NewFailureArtifacts(rawEventID, detail string, validationStatus string, now time.Time) FailureArtifacts {
	if validationStatus == "" {
		validationStatus = "failed"
	}
	if detail == "" {
		detail = "Luna normalization did not produce a valid lifecycle output"
	}
	completed := now.UTC().Format(time.RFC3339Nano)
	resultID := "luna-result-failure-" + rawEventID
	return FailureArtifacts{
		ModelRun: gen.ModelRun{
			SchemaVersion:       "1.0.0",
			ModelRunID:          "luna-run-failure-" + rawEventID,
			Agent:               "luna",
			Provider:            "ingestion-boundary",
			Model:               "not-accepted",
			PromptVersion:       "unavailable",
			OutputSchemaVersion: "1.0.0",
			InputEventIds:       []any{rawEventID},
			OutputIds:           []any{resultID},
			ValidationStatus:    validationStatus,
			FailureDetail:       detail,
			StartedAt:           completed,
			CompletedAt:         completed,
		},
		Result: gen.LunaResult{
			SchemaVersion: "1.0.0",
			LunaResultID:  resultID,
			RawEventID:    rawEventID,
			Status:        "rejected",
			Reason:        fmt.Sprintf("Luna normalization unavailable: %s", detail),
			CreatedAt:     completed,
		},
	}
}
