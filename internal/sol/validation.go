// Package sol implements the constrained, supervisor-requested briefing
// boundary for Mosaic. It has no model transport, tool, or operational-state
// dependency.
package sol

import (
	"encoding/json"
	"fmt"

	"mosaic.local/mosaic/internal/ontology"
	"mosaic.local/mosaic/internal/ontology/gen"
)

const (
	evidenceSchema       = "evidence.schema.json"
	insightSchema        = "insight.schema.json"
	recommendationSchema = "recommendation.schema.json"
	modelRunSchema       = "model-run.schema.json"
)

// SchemaValidator validates every artifact admitted at the Sol boundary. The
// composition root compiles schemas before it wires a Sol service.
type SchemaValidator struct {
	schemas map[string]ontology.Schema
}

// LoadSchemaValidator compiles the authored ontology schemas required by Sol.
func LoadSchemaValidator(schemaDir string) (*SchemaValidator, error) {
	schemas, err := ontology.CompileDir(schemaDir)
	if err != nil {
		return nil, fmt.Errorf("compile Sol schemas: %w", err)
	}
	return &SchemaValidator{schemas: schemas}, nil
}

// ValidateEvidence checks a permitted evidence reference before it is exposed
// to a structured client.
func (v *SchemaValidator) ValidateEvidence(evidence gen.Evidence) error {
	return v.validate(evidenceSchema, evidence)
}

// ValidateInsight checks an active Terra assessment before Sol may receive it.
func (v *SchemaValidator) ValidateInsight(insight gen.Insight) error {
	return v.validate(insightSchema, insight)
}

// ValidateRecommendation checks a supervisor-review option before persistence.
func (v *SchemaValidator) ValidateRecommendation(recommendation gen.Recommendation) error {
	return v.validate(recommendationSchema, recommendation)
}

// ValidateModelRun checks model-call provenance, including failures that
// intentionally produce no Recommendation.
func (v *SchemaValidator) ValidateModelRun(run gen.ModelRun) error {
	return v.validate(modelRunSchema, run)
}

func (v *SchemaValidator) validate(name string, record any) error {
	if v == nil {
		return fmt.Errorf("Sol schema validator is required")
	}
	schema, ok := v.schemas[name]
	if !ok || schema.Compiled == nil {
		return fmt.Errorf("Sol schema %q is unavailable", name)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", name, err)
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return fmt.Errorf("decode %s: %w", name, err)
	}
	if err := schema.Compiled.Validate(value); err != nil {
		return fmt.Errorf("validate %s: %w", name, err)
	}
	return nil
}
