// Package luna contains the schema boundary for structured Luna normalization
// artifacts. It does not call a model or write records.
package luna

import (
	"encoding/json"
	"fmt"

	"mosaic.local/mosaic/internal/ontology"
	"mosaic.local/mosaic/internal/ontology/gen"
)

const (
	rawEventSchema       = "raw-event.schema.json"
	canonicalEventSchema = "canonical-event.schema.json"
	lunaResultSchema     = "luna-result.schema.json"
	modelRunSchema       = "model-run.schema.json"
)

// SchemaValidator validates the records that cross the Luna normalization
// boundary. The schemas are compiled once at startup by the composition root.
type SchemaValidator struct {
	schemas map[string]ontology.Schema
}

// LoadSchemaValidator compiles the authored ontology directory for use at the
// ingestion boundary.
func LoadSchemaValidator(schemaDir string) (*SchemaValidator, error) {
	schemas, err := ontology.CompileDir(schemaDir)
	if err != nil {
		return nil, fmt.Errorf("compile Luna schemas: %w", err)
	}
	return &SchemaValidator{schemas: schemas}, nil
}

// ValidateRawEvent checks an envelope, not the opaque source payload inside it.
func (v *SchemaValidator) ValidateRawEvent(event gen.RawEvent) error {
	return v.validate(rawEventSchema, event)
}

// ValidateCanonicalEvent checks a proposed projectable normalized event before
// it is appended to the database-assigned canonical sequence.
func (v *SchemaValidator) ValidateCanonicalEvent(event gen.CanonicalEvent) error {
	return v.validate(canonicalEventSchema, event)
}

// ValidateLunaResult checks the immutable lifecycle result returned by Luna.
func (v *SchemaValidator) ValidateLunaResult(result gen.LunaResult) error {
	return v.validate(lunaResultSchema, result)
}

// ValidateModelRun checks model-call provenance, including failed and refused
// calls that intentionally produce no Canonical Event.
func (v *SchemaValidator) ValidateModelRun(run gen.ModelRun) error {
	return v.validate(modelRunSchema, run)
}

func (v *SchemaValidator) validate(name string, record any) error {
	if v == nil {
		return fmt.Errorf("Luna schema validator is required")
	}
	schema, ok := v.schemas[name]
	if !ok || schema.Compiled == nil {
		return fmt.Errorf("Luna schema %q is unavailable", name)
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
