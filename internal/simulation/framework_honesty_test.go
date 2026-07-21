package simulation_test

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// F1 framework-untouched honesty proof (HANDOFF §5 / §9).
//
// The progressive reveal is real: ingestion, projector, Terra/Sol services,
// ontology schemas, and the frozen domestic-disturbance dataset are not
// re-architected by the event-spine simulation path. Structural guards below
// fail if those packages import simulation (production) or if frozen anchors
// disappear. TestF1Section5DeterministicCorePackagesPass re-runs the
// deterministic core package tests as the §9 honesty gate.

// section5FrameworkPaths are production trees that must stay free of simulation
// imports and must remain present as the deterministic core.
var section5FrameworkPaths = []string{
	"internal/ingestion",
	"internal/reference/domesticdisturbance/state", // deterministic projector
	"internal/terra",
	"internal/sol",
	"internal/luna",
	"internal/ontology",
	"ontology",                      // authored JSON Schemas
	"datasets/domestic-disturbance", // frozen fixture integrity
}

// section5ProductionGoRoots are Go package roots under internal/ covered by §5.
// Scanned for simulation imports (deny-by-default subset of dependency direction).
var section5ProductionGoRoots = []string{
	"internal/ingestion",
	"internal/reference/domesticdisturbance/state",
	"internal/terra",
	"internal/sol",
	"internal/luna",
	"internal/ontology",
}

func TestF1Section5FrameworkPathsPresent(t *testing.T) {
	root := findRepoRoot(t)
	for _, rel := range section5FrameworkPaths {
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("Section 5 path missing %s: %v", rel, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("Section 5 path %s is not a directory", rel)
		}
	}

	// Frozen dataset integrity anchors (sha256 / id map live in manifest).
	manifest := filepath.Join(root, "datasets", "domestic-disturbance", "manifest.json")
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("frozen dataset manifest: %v", err)
	}
	rawEvents := filepath.Join(root, "datasets", "domestic-disturbance", "raw-events.json")
	if _, err := os.Stat(rawEvents); err != nil {
		t.Fatalf("frozen raw-events: %v", err)
	}
	// Ontology source of truth for canonical / insight / recommendation shapes.
	for _, schema := range []string{
		"canonical-event.schema.json",
		"insight.schema.json",
		"recommendation.schema.json",
		"raw-event.schema.json",
	} {
		p := filepath.Join(root, "ontology", schema)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("ontology schema %s: %v", schema, err)
		}
	}
}

// TestF1Section5ProductionPackagesDoNotImportSimulation is the honesty guard
// for packages listed in HANDOFF §5. It is stricter-scoped than the full
// internal/ dependency-direction scan but documents the F1 verification surface.
func TestF1Section5ProductionPackagesDoNotImportSimulation(t *testing.T) {
	root := findRepoRoot(t)
	fset := token.NewFileSet()
	var violations []string

	for _, rel := range section5ProductionGoRoots {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if parseErr != nil {
				return parseErr
			}
			relPath, relErr := filepath.Rel(root, path)
			if relErr != nil {
				relPath = path
			}
			for _, imp := range file.Imports {
				importPath := strings.Trim(imp.Path.Value, `"`)
				if isForbiddenSimulationImport(importPath) {
					violations = append(violations, filepath.ToSlash(relPath)+" imports "+importPath)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", rel, err)
		}
	}
	if len(violations) > 0 {
		t.Fatalf("Section 5 framework packages must not import simulation:\n  %s",
			strings.Join(violations, "\n  "))
	}
}

// TestF1ProgressivePathDoesNotLiveInFrameworkPackages documents that the
// progressive EventLog / BeatExecutor / session controller live under
// internal/simulation (and composition in cmd/), not inside projector/ingest
// or Terra/Sol services — the reveal is orchestration, not a faked framework.
func TestF1ProgressivePathDoesNotLiveInFrameworkPackages(t *testing.T) {
	root := findRepoRoot(t)

	// Simulation ownership anchors that F1 verification depends on.
	required := []string{
		"internal/simulation/executor.go",
		"internal/simulation/session/controller.go",
		"internal/simulation/cassette/mode.go",
		"internal/eventlog/eventlog.go",
	}
	for _, rel := range required {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("expected progressive/spine path %s: %v", rel, err)
		}
	}

	// Forbidden: BeatExecutor / session controller must not be re-hosted under
	// Section 5 production trees (would mean framework re-architecture).
	forbiddenNames := []string{
		"beat_executor.go",
		"beatexecutor.go",
		"simulation_controller.go",
	}
	for _, rel := range section5ProductionGoRoots {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			base := strings.ToLower(d.Name())
			for _, bad := range forbiddenNames {
				if base == bad {
					relPath, _ := filepath.Rel(root, path)
					t.Errorf("framework package hosts simulation type file: %s", filepath.ToSlash(relPath))
				}
			}
			return nil
		})
	}
}

// section5GoTestPatterns are the deterministic-core packages re-executed as the
// HANDOFF §5 / §9 honesty gate ("existing deterministic-core tests stay green").
var section5GoTestPatterns = []string{
	"./internal/ingestion/...",
	"./internal/terra/...",
	"./internal/sol/...",
	"./internal/luna/...",
	"./internal/ontology/...",
	"./internal/reference/domesticdisturbance/state/...",
	"./internal/reference/domesticdisturbance/dataset/...",
	"./internal/reference/domesticdisturbance/simulator/...",
}

// TestF1Section5DeterministicCorePackagesPass re-runs §5 package tests so the
// honesty claim is not only structural (path presence / import scan).
func TestF1Section5DeterministicCorePackagesPass(t *testing.T) {
	root := findRepoRoot(t)
	args := append([]string{"test", "-count=1"}, section5GoTestPatterns...)
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("Section 5 deterministic-core package tests failed: %v\n%s", err, buf.String())
	}
}
