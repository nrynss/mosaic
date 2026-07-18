package ontology

import (
	"path/filepath"
	"runtime"
	"testing"
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestSchemasAndFixtures(t *testing.T) {
	root := repositoryRoot(t)
	schemas, err := CompileDir(filepath.Join(root, "ontology"))
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) != len(SchemaFiles) {
		t.Fatalf("compiled %d schemas, want %d", len(schemas), len(SchemaFiles))
	}
	if err := ValidateFixtureDir(schemas, filepath.Join(root, "internal", "ontology", "testdata", "fixtures")); err != nil {
		t.Fatal(err)
	}
}
