package democast

import (
	"path/filepath"
	"testing"

	"mosaic.local/mosaic/internal/reference/domesticdisturbance/simulator"
)

func TestLoadDefaultManifest(t *testing.T) {
	root := repoRoot(t)
	m, err := LoadDefaultManifest(root)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if m.Scenario != "domestic-disturbance" {
		t.Fatalf("scenario = %q", m.Scenario)
	}
	if m.ExpectedCOPRevision != 9 {
		t.Fatalf("expected_cop_revision = %d", m.ExpectedCOPRevision)
	}
	if m.SupervisorIdentity != "supervisor-demo" {
		t.Fatalf("supervisor = %q", m.SupervisorIdentity)
	}
	if m.Steps[0].Kind != "play" {
		t.Fatalf("step 0 kind = %q, want play", m.Steps[0].Kind)
	}
	luna := 0
	for _, step := range m.Steps {
		if step.Kind == "luna" {
			luna++
			if step.RawEventRef == "raw-domestic-008-invalid-input" && step.ExpectedStatus != "quarantined" {
				t.Fatalf("beat-8 expected_status = %q, want quarantined", step.ExpectedStatus)
			}
		}
		if step.Kind == "terra" || step.Kind == "sol" {
			if step.StateRevision != m.ExpectedCOPRevision {
				t.Fatalf("%s state_revision = %d, want %d", step.Kind, step.StateRevision, m.ExpectedCOPRevision)
			}
		}
	}
	if luna != 10 {
		t.Fatalf("luna steps = %d, want 10", luna)
	}
}

func TestManifestValidatePlayFirstAndRevAlignment(t *testing.T) {
	base := Manifest{
		Scenario:            "domestic-disturbance",
		ExpectedCOPRevision: 9,
		SupervisorIdentity:  "supervisor-demo",
		Steps: []Step{
			{Kind: "play"},
			{Kind: "luna", BeatID: "b1", RawEventRef: "r1"},
			{Kind: "luna", BeatID: "b2", RawEventRef: "r2"},
			{Kind: "luna", BeatID: "b3", RawEventRef: "r3"},
			{Kind: "luna", BeatID: "b4", RawEventRef: "r4"},
			{Kind: "luna", BeatID: "b5", RawEventRef: "r5"},
			{Kind: "luna", BeatID: "b6", RawEventRef: "r6"},
			{Kind: "luna", BeatID: "b7", RawEventRef: "r7"},
			{Kind: "luna", BeatID: "b8", RawEventRef: "r8", ExpectedStatus: "quarantined"},
			{Kind: "luna", BeatID: "b9", RawEventRef: "r9"},
			{Kind: "luna", BeatID: "b10", RawEventRef: "r10"},
			{Kind: "terra", StateRevision: 9, Evidence: []EvidenceLiteral{{Kind: "raw_event", ID: "r1", Explanation: "e"}}},
			{Kind: "sol", StateRevision: 9, Insights: []InsightRef{{InsightID: "i1"}}, Evidence: []EvidenceLiteral{{Kind: "raw_event", ID: "r1", Explanation: "e"}}},
		},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid base: %v", err)
	}

	// Play not first.
	bad := base
	bad.Steps = append([]Step{{Kind: "luna", BeatID: "x", RawEventRef: "r"}}, base.Steps...)
	if err := bad.Validate(); err == nil {
		t.Fatal("expected error when play is not first")
	}

	// Terra revision misaligned.
	bad = base
	bad.Steps = append([]Step{}, base.Steps...)
	for i := range bad.Steps {
		if bad.Steps[i].Kind == "terra" {
			bad.Steps[i].StateRevision = 7
		}
	}
	if err := bad.Validate(); err == nil {
		t.Fatal("expected error when terra state_revision != expected_cop_revision")
	}

	// Sol revision misaligned.
	bad = base
	bad.Steps = append([]Step{}, base.Steps...)
	for i := range bad.Steps {
		if bad.Steps[i].Kind == "sol" {
			bad.Steps[i].StateRevision = 1
		}
	}
	if err := bad.Validate(); err == nil {
		t.Fatal("expected error when sol state_revision != expected_cop_revision")
	}

	// Bad expected_status.
	bad = base
	bad.Steps = append([]Step{}, base.Steps...)
	bad.Steps[1].ExpectedStatus = "banana"
	if err := bad.Validate(); err == nil {
		t.Fatal("expected error for unknown luna expected_status")
	}
}

func TestAssertOperatorOKStrictQuarantine(t *testing.T) {
	// CI path: ok must not soft-pass when expected is quarantined.
	err := AssertOperatorOK(StepResult{
		Kind:         "luna",
		RawEventID:   "raw-domestic-008-invalid-input",
		Status:       "ok",
		ExpectedLuna: "quarantined",
	}, true)
	if err == nil {
		t.Fatal("expected strict CI failure when status ok but want quarantined")
	}

	// CI path: exact match green.
	if err := AssertOperatorOK(StepResult{
		Kind:         "luna",
		RawEventID:   "raw-domestic-008-invalid-input",
		Status:       "quarantined",
		ExpectedLuna: "quarantined",
		Provider:     "mosaic-fixture",
	}, true); err != nil {
		t.Fatalf("strict quarantined: %v", err)
	}

	// Live path: ok is acceptable even if ExpectedLuna is quarantined
	// (WARN is logged by the live test; CI is the gate after bank update).
	if err := AssertOperatorOK(StepResult{
		Kind:         "luna",
		RawEventID:   "raw-domestic-008-invalid-input",
		Status:       "ok",
		ExpectedLuna: "quarantined",
	}, false); err != nil {
		t.Fatalf("live soft path: %v", err)
	}
}

func TestLoadRawEventsCoversManifest(t *testing.T) {
	root := repoRoot(t)
	m, err := LoadDefaultManifest(root)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	idx, err := LoadRawEvents(root, m.Scenario)
	if err != nil {
		t.Fatalf("raw events: %v", err)
	}
	for _, step := range m.Steps {
		if step.Kind != "luna" {
			continue
		}
		if _, err := idx.Get(step.RawEventRef); err != nil {
			t.Fatalf("resolve %s: %v", step.RawEventRef, err)
		}
	}
	if _, err := idx.Get("missing"); err == nil {
		t.Fatal("expected miss")
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := simulator.RepositoryRoot(".")
	if err != nil {
		// Walk up from testdata-relative cwd if needed.
		root, err = simulator.RepositoryRoot(filepath.Join("..", ".."))
		if err != nil {
			t.Fatalf("repository root: %v", err)
		}
	}
	return root
}
