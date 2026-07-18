package dataset

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mosaic.local/mosaic/internal/ontology"
)

func TestValidDomesticDisturbanceArtifacts(t *testing.T) {
	root := repositoryRoot(t)
	schemas, err := ontology.CompileDir(filepath.Join(root, "ontology"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateArtifacts(schemas, filepath.Join(root, "datasets", DomesticDisturbance)); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsMalformedSchemaReferenceAndExpectedOutcome(t *testing.T) {
	root := repositoryRoot(t)
	schemas, err := ontology.CompileDir(filepath.Join(root, "ontology"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name        string
		file        string
		old         string
		replacement string
	}{
		{"malformed schema artifact", "manifest.json", `"schema_version": "1.0.0"`, `"schema_version": "0.0.0"`},
		{"missing manifest reference", "scenario.json", `"dataset_manifest_id": "manifest-domestic-disturbance-v1"`, `"dataset_manifest_id": "manifest-does-not-exist"`},
		{"malformed expected outcome", "expected-outcomes.json", `"canonical_event_id": "canonical-domestic-008-late-ems"`, `"canonical_event_id": ""`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			dir := copyDataset(t, root)
			path := filepath.Join(dir, test.file)
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			changed := strings.Replace(string(content), test.old, test.replacement, 1)
			if changed == string(content) {
				t.Fatalf("mutation did not apply to %s", test.file)
			}
			if err := os.WriteFile(path, []byte(changed), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := ValidateArtifacts(schemas, dir); err == nil {
				t.Fatal("ValidateArtifacts accepted malformed artifacts")
			}
		})
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func copyDataset(t *testing.T, root string) string {
	t.Helper()
	source := filepath.Join(root, "datasets", DomesticDisturbance)
	target := filepath.Join(t.TempDir(), DomesticDisturbance)
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, entry.Name()), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return target
}
