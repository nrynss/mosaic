// Package terra implements the constrained, structured assessment boundary for
// Mosaic. It has no model transport, tool, or operational-state dependency.
package terra

import (
	"encoding/json"
	"fmt"

	"mosaic.local/mosaic/internal/ontology"
	"mosaic.local/mosaic/internal/ontology/gen"
)

const (
	evidenceSchema = "evidence.schema.json"
	insightSchema  = "insight.schema.json"
	modelRunSchema = "model-run.schema.json"
)

// SchemaValidator validates every record admitted at the Terra boundary. The
// composition root compiles schemas before it wires a Terra service.
type SchemaValidator struct {
	schemas map[string]ontology.Schema
}

// LoadSchemaValidator compiles the authored ontology schemas for Terra.
func LoadSchemaValidator(schemaDir string) (*SchemaValidator, error) {
	schemas, err := ontology.CompileDir(schemaDir)
	if err != nil {
		return nil, fmt.Errorf("compile Terra schemas: %w", err)
	}
	return &SchemaValidator{schemas: schemas}, nil
}

// ValidateEvidence checks a permitted evidence reference before it is exposed
// to a structured client.
func (v *SchemaValidator) ValidateEvidence(evidence gen.Evidence) error {
	return v.validate(evidenceSchema, evidence)
}

// ValidateInsight checks a derived assessment before it can be persisted.
func (v *SchemaValidator) ValidateInsight(insight gen.Insight) error {
	return v.validate(insightSchema, insight)
}

// ValidateModelRun checks model-call provenance, including a refusal or
// failure that intentionally produces no Insight.
func (v *SchemaValidator) ValidateModelRun(run gen.ModelRun) error {
	return v.validate(modelRunSchema, run)
}

func (v *SchemaValidator) validate(name string, record any) error {
	if v == nil {
		return fmt.Errorf("Terra schema validator is required")
	}
	schema, ok := v.schemas[name]
	if !ok || schema.Compiled == nil {
		return fmt.Errorf("Terra schema %q is unavailable", name)
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
