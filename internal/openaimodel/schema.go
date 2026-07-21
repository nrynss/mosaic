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

// loadLunaStructuredOutputSchema builds a purpose-designed OpenAI strict-mode
// wire schema for Luna. The model returns {result, canonical_event|null}.
//
// Authored ontology schemas remain the source of truth for local validation and
// are never modified. The wire document is self-contained: one root $defs table,
// no $id base-URI re-scoping, no allOf/if/then (forbidden by OpenAI strict mode),
// and no typeless nodes. Payload discrimination and status-conditional required
// fields are re-enforced by Mosaic's authored-schema validators after the call.
func loadLunaStructuredOutputSchema(schemaDir string) (structuredOutputSchema, error) {
	if !responseFormatName.MatchString(lunaResultSchemaRoute.name) {
		return structuredOutputSchema{}, fmt.Errorf("unsafe structured-output schema name %q", lunaResultSchemaRoute.name)
	}
	lunaResult, err := readAuthoredSchema(schemaDir, lunaResultSchemaRoute.file)
	if err != nil {
		return structuredOutputSchema{}, err
	}
	canonicalEvent, err := readAuthoredSchema(schemaDir, "canonical-event.schema.json")
	if err != nil {
		return structuredOutputSchema{}, err
	}
	document, err := buildLunaStrictWireSchema(lunaResult, canonicalEvent)
	if err != nil {
		return structuredOutputSchema{}, err
	}
	return structuredOutputSchema{name: lunaResultSchemaRoute.name, document: document}, nil
}

// buildLunaStrictWireSchema composes a single strict-compatible JSON Schema
// document from the authored LunaResult + CanonicalEvent schemas.
func buildLunaStrictWireSchema(lunaResult, canonicalEvent json.RawMessage) (json.RawMessage, error) {
	lunaRoot, err := decodeSchemaObject(lunaResult, "luna-result")
	if err != nil {
		return nil, err
	}
	canonRoot, err := decodeSchemaObject(canonicalEvent, "canonical-event")
	if err != nil {
		return nil, err
	}

	rootDefs := map[string]any{}

	// Namespace each schema's $defs into the wrapper root so #/$defs/... refs
	// resolve against one table (embedding two $id documents would re-scope
	// fragment refs and break OpenAI's resolver).
	if err := hoistDefs(lunaRoot, "luna_", rootDefs); err != nil {
		return nil, fmt.Errorf("hoist Luna $defs: %w", err)
	}
	if err := hoistDefs(canonRoot, "canon_", rootDefs); err != nil {
		return nil, fmt.Errorf("hoist canonical $defs: %w", err)
	}

	// Strip OpenAI-forbidden / wire-unneeded keywords from the embedded bodies.
	// allOf/if/then express status and event_type discrimination that Mosaic
	// re-validates after the call; keeping them on the wire yields HTTP 400.
	stripWireIncompatibleKeywords(lunaRoot)
	stripWireIncompatibleKeywords(canonRoot)

	// repair.fields[].original (and replacement) are authored as bare {}
	// ("any"). Strict mode requires a type; a scalar union is enough because
	// authored validation accepts the real value afterward.
	if err := typeAnyLeavesInDefs(rootDefs); err != nil {
		return nil, err
	}

	// Represent the payload discriminated union as anyOf of concrete payload
	// shapes instead of allOf/if/then on event_type.
	if err := expandCanonicalPayloadAnyOf(canonRoot, "canon_"); err != nil {
		return nil, err
	}

	// Rewrite fragment refs to the namespaced root $defs keys. Bodies first,
	// then each def under its own prefix (defs still use un-prefixed fragments
	// until this pass — e.g. confidence → #/$defs/strength becomes
	// #/$defs/canon_strength).
	rewriteDefRefs(lunaRoot, "luna_")
	rewriteDefRefs(canonRoot, "canon_")
	for name, def := range rootDefs {
		switch {
		case strings.HasPrefix(name, "luna_"):
			rewriteDefRefs(def, "luna_")
		case strings.HasPrefix(name, "canon_"):
			rewriteDefRefs(def, "canon_")
		}
	}

	// Drop metadata that must not appear on the wire document.
	for _, meta := range []string{"$schema", "$id", "title", "$defs"} {
		delete(lunaRoot, meta)
		delete(canonRoot, meta)
	}

	wrapper := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"result", "canonical_event"},
		"properties": map[string]any{
			"result": lunaRoot,
			"canonical_event": map[string]any{
				"anyOf": []any{canonRoot, map[string]any{"type": "null"}},
			},
		},
		"$defs": rootDefs,
	}

	if err := makeSchemaStrict(wrapper); err != nil {
		return nil, fmt.Errorf("make Luna wire schema strict-compatible: %w", err)
	}
	encoded, err := json.Marshal(wrapper)
	if err != nil {
		return nil, fmt.Errorf("encode Luna structured-output wrapper: %w", err)
	}
	return encoded, nil
}

func decodeSchemaObject(document json.RawMessage, label string) (map[string]any, error) {
	var root map[string]any
	if err := json.Unmarshal(document, &root); err != nil {
		return nil, fmt.Errorf("decode %s schema: %w", label, err)
	}
	// Deep-copy via re-marshal so mutations never touch shared/cached input.
	// Callers pass freshly read bytes, but keep the transform hermetic.
	encoded, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("clone %s schema: %w", label, err)
	}
	var clone map[string]any
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return nil, fmt.Errorf("clone %s schema: %w", label, err)
	}
	return clone, nil
}

func hoistDefs(root map[string]any, prefix string, into map[string]any) error {
	defs, ok := root["$defs"].(map[string]any)
	if !ok || len(defs) == 0 {
		return nil
	}
	for name, def := range defs {
		key := prefix + name
		if _, exists := into[key]; exists {
			return fmt.Errorf("duplicate $defs key %q", key)
		}
		into[key] = def
	}
	return nil
}

func stripWireIncompatibleKeywords(node any) {
	switch n := node.(type) {
	case map[string]any:
		for _, key := range []string{"allOf", "if", "then", "else", "not", "dependentRequired", "dependentSchemas"} {
			delete(n, key)
		}
		for _, child := range n {
			stripWireIncompatibleKeywords(child)
		}
	case []any:
		for _, child := range n {
			stripWireIncompatibleKeywords(child)
		}
	}
}

// typeAnyLeavesInDefs replaces typeless accept-any objects ({}) under $defs
// with a concrete scalar union OpenAI strict mode accepts.
func typeAnyLeavesInDefs(defs map[string]any) error {
	scalarAny := map[string]any{
		"type": []any{"string", "number", "integer", "boolean", "null"},
	}
	var walk func(any)
	walk = func(value any) {
		switch n := value.(type) {
		case map[string]any:
			// A bare {} (no type, no keywords that pin a shape) is accept-any.
			if isTypelessAnyObject(n) {
				// Mutate in place: clear and set type union.
				for k := range n {
					delete(n, k)
				}
				n["type"] = scalarAny["type"]
				return
			}
			for _, child := range n {
				walk(child)
			}
		case []any:
			for _, child := range n {
				walk(child)
			}
		}
	}
	walk(defs)
	return nil
}

func isTypelessAnyObject(node map[string]any) bool {
	if len(node) == 0 {
		return true
	}
	// Only metadata-less empty constraints count as "any".
	for key := range node {
		switch key {
		case "description", "title", "default":
			continue
		default:
			return false
		}
	}
	return true
}

// expandCanonicalPayloadAnyOf replaces the opaque payload object with an anyOf
// of the concrete per-event_type payload defs (incident, unit, road, ...).
func expandCanonicalPayloadAnyOf(canonRoot map[string]any, prefix string) error {
	properties, ok := canonRoot["properties"].(map[string]any)
	if !ok {
		return fmt.Errorf("canonical event schema missing properties")
	}
	payloadNames := []string{
		"incident_payload",
		"incident_resolved_payload",
		"unit_payload",
		"resource_payload",
		"road_payload",
		"weather_payload",
	}
	variants := make([]any, 0, len(payloadNames))
	for _, name := range payloadNames {
		variants = append(variants, map[string]any{
			"$ref": "#/$defs/" + prefix + name,
		})
	}
	properties["payload"] = map[string]any{"anyOf": variants}
	return nil
}

// rewriteDefRefs rewrites #/$defs/<name> fragment refs by prepending prefix to
// the def name. Refs that already include the prefix, or point outside $defs,
// are left unchanged.
func rewriteDefRefs(value any, prefix string) {
	if prefix == "" {
		return
	}
	switch n := value.(type) {
	case map[string]any:
		if ref, ok := n["$ref"].(string); ok {
			const head = "#/$defs/"
			if strings.HasPrefix(ref, head) {
				name := strings.TrimPrefix(ref, head)
				if name != "" && !strings.HasPrefix(name, prefix) {
					n["$ref"] = head + prefix + name
				}
			}
		}
		for _, child := range n {
			rewriteDefRefs(child, prefix)
		}
	case []any:
		for _, child := range n {
			rewriteDefRefs(child, prefix)
		}
	}
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
		inferStrictLeafType(node)
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

// inferStrictLeafType assigns a JSON Schema "type" to a const/enum leaf node
// that omits one. OpenAI strict structured-output mode requires every schema
// node to declare a type, but an authored ontology schema legally omits it when
// a const or enum already pins the value (e.g. "schema_version": {"const":
// "1.0.0"} or a "low"/"medium"/"high" enum). Without this, OpenAI rejects the
// whole request with HTTP 400 invalid_json_schema. The authored schema is never
// modified; only this wire copy gains the type OpenAI demands. A mixed-type enum
// becomes a JSON Schema type array so the constraint stays honest.
func inferStrictLeafType(node map[string]any) {
	if _, ok := node["type"]; ok {
		return
	}
	var samples []any
	if constValue, ok := node["const"]; ok {
		samples = append(samples, constValue)
	}
	if enumValues, ok := node["enum"].([]any); ok {
		samples = append(samples, enumValues...)
	}
	if len(samples) == 0 {
		return
	}
	seen := map[string]bool{}
	var ordered []string
	for _, sample := range samples {
		name := jsonSchemaTypeName(sample)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		ordered = append(ordered, name)
	}
	switch len(ordered) {
	case 0:
		// No inferable scalar type; leave untouched rather than guess.
	case 1:
		node["type"] = ordered[0]
	default:
		types := make([]any, len(ordered))
		for i, name := range ordered {
			types[i] = name
		}
		node["type"] = types
	}
}

// jsonSchemaTypeName maps a decoded JSON scalar to its JSON Schema type name.
// JSON numbers decode as float64; a whole value reports "integer" to match
// authored integer constraints, everything else "number".
func jsonSchemaTypeName(value any) string {
	switch typed := value.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		if typed == float64(int64(typed)) {
			return "integer"
		}
		return "number"
	case nil:
		return "null"
	default:
		return ""
	}
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
				// Luna repair.fields[].original is required by the authored schema
				// and may intentionally record a missing source value as null.
				if key == "original" {
					continue
				}
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
