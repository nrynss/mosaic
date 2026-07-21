package simulation_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// frameworkRoots are package path prefixes under the module that must never
// import internal/simulation (or the retired internal/simsession path).
// Composition roots (cmd/) and this tree may import simulation; tests under
// framework packages may also wire the real controller, so only non-test
// production sources are scanned.
var frameworkRoots = []string{
	"internal/ingestion",
	"internal/store",
	"internal/terra",
	"internal/sol",
	"internal/luna",
	"internal/ontology",
	"internal/contracts",
	"internal/stream",
	"internal/replay",
	"internal/api",
	"internal/reference/domesticdisturbance/state",
	"internal/reference/domesticdisturbance/dataset",
	"internal/reference/registry",
	"internal/openaimodel",
	"internal/usage",
	"internal/profile",
	"internal/recurrence",
	"internal/datasetgen",
}

const (
	modulePath          = "mosaic.local/mosaic"
	forbiddenSimulation = modulePath + "/internal/simulation"
	forbiddenSimSession = modulePath + "/internal/simsession"
)

func TestFrameworkPackagesDoNotImportSimulation(t *testing.T) {
	repoRoot := findRepoRoot(t)
	fset := token.NewFileSet()
	var violations []string

	for _, root := range frameworkRoots {
		absRoot := filepath.Join(repoRoot, filepath.FromSlash(root))
		info, err := os.Stat(absRoot)
		if err != nil || !info.IsDir() {
			continue
		}
		err = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				name := d.Name()
				if name == "testdata" || name == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			base := filepath.Base(path)
			if strings.HasSuffix(base, "_test.go") {
				// API and other framework tests may compose the real session
				// controller; production sources must stay simulation-free.
				return nil
			}
			file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if parseErr != nil {
				return parseErr
			}
			rel, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				rel = path
			}
			for _, imp := range file.Imports {
				importPath := strings.Trim(imp.Path.Value, `"`)
				if isForbiddenSimulationImport(importPath) {
					violations = append(violations, filepath.ToSlash(rel)+" imports "+importPath)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}

	if len(violations) > 0 {
		t.Fatalf("framework packages must not import simulation (dependency direction):\n  %s",
			strings.Join(violations, "\n  "))
	}
}

func TestNoPackageImportsRetiredSimsessionPath(t *testing.T) {
	repoRoot := findRepoRoot(t)
	fset := token.NewFileSet()
	var violations []string

	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			switch name {
			case ".git", "node_modules", "ui", "localmodels", "vendor", "pw-e2e", ".claude", ".worktrees", "worktrees":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			rel = path
		}
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if importPath == forbiddenSimSession || strings.HasPrefix(importPath, forbiddenSimSession+"/") {
				violations = append(violations, filepath.ToSlash(rel)+" imports "+importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("retired import path internal/simsession still referenced:\n  %s",
			strings.Join(violations, "\n  "))
	}
}

func isForbiddenSimulationImport(importPath string) bool {
	return importPath == forbiddenSimulation ||
		strings.HasPrefix(importPath, forbiddenSimulation+"/") ||
		importPath == forbiddenSimSession ||
		strings.HasPrefix(importPath, forbiddenSimSession+"/")
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// this file: <repo>/internal/simulation/dependency_direction_test.go
	dir := filepath.Dir(file)
	root := filepath.Clean(filepath.Join(dir, "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %s has no go.mod: %v", root, err)
	}
	return root
}
