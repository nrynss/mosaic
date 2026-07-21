package openaimodel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Structured-output names are stable, safe API identifiers. They deliberately
// do not reuse ontology URLs, which are schema identifiers rather than the
// Responses API's limited name field.
const (
	insightSchemaName        = "mosaic_insight_v1_0_0"
	recommendationSchemaName = "mosaic_recommendation_v1_0_0"
	lunaSchemaName           = "mosaic_luna_normalization_v1_0_0"
)

var responseFormatName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type schemaRoute struct {
	name string
	file string
}

var (
	insightSchemaRoute        = schemaRoute{name: insightSchemaName, file: "insight.schema.json"}
	recommendationSchemaRoute = schemaRoute{name: recommendationSchemaName, file: "recommendation.schema.json"}
	lunaResultSchemaRoute     = schemaRoute{name: lunaSchemaName, file: "luna-result.schema.json"}
)

type structuredOutputSchema struct {
	name     string
	document json.RawMessage
}

func loadStructuredOutputSchema(schemaDir string, route schemaRoute) (structuredOutputSchema, error) {
	if !responseFormatName.MatchString(route.name) {
		return structuredOutputSchema{}, fmt.Errorf("unsafe structured-output schema name %q", route.name)
	}
	document, err := readAuthoredSchema(schemaDir, route.file)
	if err != nil {
		return structuredOutputSchema{}, err
	}
	document, err = strictCompatibleSchema(document)
	if err != nil {
		return structuredOutputSchema{}, fmt.Errorf("make output schema %s strict-compatible: %w", route.file, err)
	}
	return structuredOutputSchema{name: route.name, document: document}, nil
}

// loadLunaStructuredOutputSchema preserves the existing Luna adapter contract:
// the model returns a LunaResult plus an optional CanonicalEvent. The authored
// schemas are transformed only for OpenAI's strict wire requirements; Mosaic's
// authored schemas remain the source of truth for all local validation.
func loadLunaStructuredOutputSchema(schemaDir string) (structuredOutputSchema, error) {
	if !responseFormatName.MatchString(lunaResultSchemaRoute.name) {
		return structuredOutputSchema{}, fmt.Errorf("unsafe structured-output schema name %q", lunaResultSchemaRoute.name)
	}
	lunaResult, err := readAuthoredSchema(schemaDir, lunaResultSchemaRoute.file)
	if err != nil {
		return structuredOutputSchema{}, err
	}
	lunaResult, err = strictCompatibleSchema(lunaResult)
	if err != nil {
		return structuredOutputSchema{}, fmt.Errorf("make Luna result schema strict-compatible: %w", err)
	}
	canonicalEvent, err := readAuthoredSchema(schemaDir, "canonical-event.schema.json")
	if err != nil {
		return structuredOutputSchema{}, err
	}
	canonicalEvent, err = strictCompatibleSchema(canonicalEvent)
	if err != nil {
		return structuredOutputSchema{}, fmt.Errorf("make canonical event schema strict-compatible: %w", err)
	}

	// CanonicalEvent is nullable for quarantined/rejected Luna outcomes; accepted
	// and repaired outcomes remain checked by the existing ingestion validator.
	wrapper := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"result", "canonical_event"},
		"properties": map[string]any{
			"result": json.RawMessage(lunaResult),
			"canonical_event": map[string]any{
				"anyOf": []any{json.RawMessage(canonicalEvent), map[string]any{"type": "null"}},
			},
		},
	}
	document, err := json.Marshal(wrapper)
	if err != nil {
		return structuredOutputSchema{}, fmt.Errorf("encode Luna structured-output wrapper: %w", err)
	}
	return structuredOutputSchema{name: lunaResultSchemaRoute.name, document: document}, nil
}

func readAuthoredSchema(schemaDir, filename string) (json.RawMessage, error) {
	if strings.TrimSpace(schemaDir) == "" {
		return nil, fmt.Errorf("ontology schema directory is required")
	}
	path := filepath.Join(schemaDir, filename)
	document, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read output schema %s: %w", filename, err)
	}
	var root map[string]any
	if err := json.Unmarshal(document, &root); err != nil {
		return nil, fmt.Errorf("decode output schema %s: %w", filename, err)
	}
	if root["type"] != "object" {
		return nil, fmt.Errorf("output schema %s must be an object schema", filename)
	}
	id, _ := root["$id"].(string)
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("output schema %s requires a non-empty $id", filename)
	}
	return append(json.RawMessage(nil), document...), nil
}

// strictCompatibleSchema derives an OpenAI strict-mode schema from an authored
// ontology schema. Strict mode requires every object property to be required,
// so properties that are semantically optional in the authored schema become
// nullable on the wire. Null placeholders are removed before Mosaic validates
// model output against the unchanged authored schema.
func strictCompatibleSchema(document json.RawMessage) (json.RawMessage, error) {
	var root any
	if err := json.Unmarshal(document, &root); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}
	if err := makeSchemaStrict(root); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("encode strict-compatible schema: %w", err)
	}
	return encoded, nil
}

func makeSchemaStrict(value any) error {
	switch node := value.(type) {
	case map[string]any:
		properties, hasProperties := node["properties"].(map[string]any)
		if hasProperties {
			required := requiredPropertyNames(node["required"])
			if _, ok := node["type"]; !ok {
				node["type"] = "object"
			}
			node["additionalProperties"] = false
			keys := make([]string, 0, len(properties))
			for key, property := range properties {
				keys = append(keys, key)
				if !required[key] {
					properties[key] = nullableSchema(property)
				}
			}
			sort.Strings(keys)
			node["required"] = keys
		}
		for _, child := range node {
			if err := makeSchemaStrict(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range node {
			if err := makeSchemaStrict(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func requiredPropertyNames(raw any) map[string]bool {
	required := map[string]bool{}
	values, ok := raw.([]any)
	if !ok {
		return required
	}
	for _, value := range values {
		name, ok := value.(string)
		if ok {
			required[name] = true
		}
	}
	return required
}

func nullableSchema(schema any) any {
	node, ok := schema.(map[string]any)
	if !ok {
		return map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}
	}
	if rawType, ok := node["type"]; ok {
		switch typed := rawType.(type) {
		case string:
			node["type"] = []any{typed, "null"}
		case []any:
			for _, value := range typed {
				if value == "null" {
					return node
				}
			}
			node["type"] = append(typed, "null")
		}
		return node
	}
	return map[string]any{"anyOf": []any{node, map[string]any{"type": "null"}}}
}

func withoutNullObjectProperties(document json.RawMessage) (json.RawMessage, error) {
	if len(document) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(document, &value); err != nil {
		return nil, fmt.Errorf("decode structured output: %w", err)
	}
	if !removeNullObjectProperties(value) {
		return append(json.RawMessage(nil), document...), nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode normalized structured output: %w", err)
	}
	return encoded, nil
}

func removeNullObjectProperties(value any) bool {
	changed := false
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			if child == nil {
				delete(node, key)
				changed = true
				continue
			}
			changed = removeNullObjectProperties(child) || changed
		}
	case []any:
		for _, child := range node {
			changed = removeNullObjectProperties(child) || changed
		}
	}
	return changed
}
