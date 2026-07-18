// Package ontology validates authored Mosaic ontology schemas and their fixtures.
package ontology

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// SchemaFiles is the complete v1 ontology surface described by RFC-0001.
var SchemaFiles = []string{
	"audit-record.schema.json",
	"canonical-event.schema.json",
	"checkpoint.schema.json",
	"dataset-manifest.schema.json",
	"evidence.schema.json",
	"incident.schema.json",
	"insight.schema.json",
	"location.schema.json",
	"luna-result.schema.json",
	"model-run.schema.json",
	"raw-event.schema.json",
	"recommendation.schema.json",
	"resource.schema.json",
	"road.schema.json",
	"scenario.schema.json",
	"unit.schema.json",
	"weather.schema.json",
}

var semver = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// Schema contains the immutable metadata and compiled JSON Schema for one record.
type Schema struct {
	File     string
	ID       string
	Version  string
	Compiled *jsonschema.Schema
}

// CompileDir validates immutable schema metadata, references, and JSON Schema syntax.
func CompileDir(dir string) (map[string]Schema, error) {
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	loaded := make([]Schema, 0, len(SchemaFiles))
	seenIDs := make(map[string]string, len(SchemaFiles))

	for _, file := range SchemaFiles {
		path := filepath.Join(dir, file)
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}

		var document map[string]any
		if err := json.Unmarshal(content, &document); err != nil {
			return nil, fmt.Errorf("decode %s: %w", file, err)
		}
		id, _ := document["$id"].(string)
		title, _ := document["title"].(string)
		version, err := schemaVersion(document)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		if id == "" || title == "" {
			return nil, fmt.Errorf("%s: require non-empty $id and title", file)
		}
		if !semver.MatchString(version) {
			return nil, fmt.Errorf("%s: schema_version %q is not semantic versioning", file, version)
		}
		if !strings.HasSuffix(id, "/v"+version) {
			return nil, fmt.Errorf("%s: $id %q must end in /v%s", file, id, version)
		}
		if prior, exists := seenIDs[id]; exists {
			return nil, fmt.Errorf("duplicate schema $id %q in %s and %s", id, prior, file)
		}
		seenIDs[id] = file
		if err := compiler.AddResource(id, document); err != nil {
			return nil, fmt.Errorf("register %s: %w", file, err)
		}
		loaded = append(loaded, Schema{File: file, ID: id, Version: version})
	}

	compiled := make(map[string]Schema, len(loaded))
	for _, item := range loaded {
		sch, err := compiler.Compile(item.ID)
		if err != nil {
			return nil, fmt.Errorf("compile %s: %w", item.File, err)
		}
		item.Compiled = sch
		compiled[item.File] = item
	}
	return compiled, nil
}

func schemaVersion(document map[string]any) (string, error) {
	properties, ok := document["properties"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("require properties.schema_version")
	}
	versionProperty, ok := properties["schema_version"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("require properties.schema_version")
	}
	version, ok := versionProperty["const"].(string)
	if !ok || version == "" {
		return "", fmt.Errorf("schema_version must be a non-empty const")
	}
	return version, nil
}

// ValidateFixtureDir validates valid fixtures, checks canonical JSON round trips,
// and proves invalid fixtures are rejected. Fixture names are <schema>.valid.json
// or <schema>.invalid.json where <schema> is the schema filename without .schema.json.
func ValidateFixtureDir(schemas map[string]Schema, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read fixture directory: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	if len(entries) == 0 {
		return fmt.Errorf("fixture directory %s is empty", dir)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := entry.Name()
		valid := strings.HasSuffix(name, ".valid.json")
		invalid := strings.HasSuffix(name, ".invalid.json")
		if !valid && !invalid {
			return fmt.Errorf("fixture %s must end .valid.json or .invalid.json", name)
		}
		base := strings.TrimSuffix(strings.TrimSuffix(name, ".valid.json"), ".invalid.json")
		schema, ok := schemas[base+".schema.json"]
		if !ok {
			return fmt.Errorf("fixture %s has no schema", name)
		}
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("read fixture %s: %w", name, err)
		}
		var value any
		if err := json.Unmarshal(content, &value); err != nil {
			return fmt.Errorf("decode fixture %s: %w", name, err)
		}
		err = schema.Compiled.Validate(value)
		if invalid {
			if err == nil {
				return fmt.Errorf("invalid fixture %s was accepted", name)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("valid fixture %s rejected: %w", name, err)
		}
		roundTrip, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("marshal fixture %s: %w", name, err)
		}
		if !json.Valid(roundTrip) || bytes.Equal(roundTrip, nil) {
			return fmt.Errorf("fixture %s did not produce valid JSON", name)
		}
		var roundTripValue any
		if err := json.Unmarshal(roundTrip, &roundTripValue); err != nil {
			return fmt.Errorf("decode round trip %s: %w", name, err)
		}
		if err := schema.Compiled.Validate(roundTripValue); err != nil {
			return fmt.Errorf("round-trip fixture %s rejected: %w", name, err)
		}
	}
	return nil
}
