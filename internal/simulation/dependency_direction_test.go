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

// Dependency direction (C1): framework and domain packages never import
// internal/simulation. Composition roots (cmd/) may. Production sources under
// internal/ are scanned deny-by-default so new packages (e.g. eventlog) are
// covered without maintaining a stale allowlist.
//
// Scanner rules:
//   - Walks every non-test .go file under internal/ except the simulation/
//     subtree itself (which may import its own subpackages).
//   - *_test.go files are exempt so framework tests can wire the real controller.
//   - Forbidden paths: mosaic.local/mosaic/internal/simulation and any
//     subpackage, plus the retired internal/simsession path.
//
// Tests (*_test.go) under framework packages may import simulation for wiring
// the real controller; only non-test production sources are restricted.

const (
	modulePath          = "mosaic.local/mosaic"
	forbiddenSimulation = modulePath + "/internal/simulation"
	forbiddenSimSession = modulePath + "/internal/simsession"
)

func TestInternalPackagesDoNotImportSimulation(t *testing.T) {
	repoRoot := findRepoRoot(t)
	internalRoot := filepath.Join(repoRoot, "internal")
	simulationRoot := filepath.Join(internalRoot, "simulation")

	fset := token.NewFileSet()
	var violations []string

	err := filepath.WalkDir(internalRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == "testdata" || name == "vendor" {
				return filepath.SkipDir
			}
			// Simulation tree may import itself; skip the whole subtree.
			if path == simulationRoot {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasSuffix(base, "_test.go") {
			// Framework tests may compose the real session controller.
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
		t.Fatalf("walk internal/: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("packages under internal/ (outside simulation) must not import simulation:\n  %s",
			strings.Join(violations, "\n  "))
	}
}

func TestNoPackageImportsRetiredSimsessionPath(t *testing.T) {
	repoRoot := findRepoRoot(t)
	fset := token.NewFileSet()
	var violations []string
	var parseErrors []string

	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			switch name {
			case ".git", "node_modules", "ui", "localmodels", "vendor", "pw-e2e",
				".claude", ".worktrees", "worktrees":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			rel, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				rel = path
			}
			parseErrors = append(parseErrors, filepath.ToSlash(rel)+": "+parseErr.Error())
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
	if len(parseErrors) > 0 {
		t.Fatalf("failed to parse Go files while checking retired simsession path:\n  %s",
			strings.Join(parseErrors, "\n  "))
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
