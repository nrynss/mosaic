package democast

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultManifestRel is the repository-relative path of the committed demo
// recording manifest.
const DefaultManifestRel = "testdata/demo/recording-manifest.json"

// DefaultCassetteDirRel is the repository-relative path of the committed
// cassette bank used by no-live CI replay.
const DefaultCassetteDirRel = "testdata/demo/cassettes"

// Manifest is the single source of truth for every scripted demo operator step.
// All fingerprint-bearing fields are literal constants — no runtime free text.
type Manifest struct {
	Scenario            string `json:"scenario"`
	ExpectedCOPRevision int64  `json:"expected_cop_revision"`
	SupervisorIdentity  string `json:"supervisor_identity"`
	Steps               []Step `json:"steps"`
}

// Step is one scripted action in demo order.
type Step struct {
	Kind string `json:"kind"` // play | luna | terra | sol

	// Luna
	BeatID      string `json:"beat_id,omitempty"`
	RawEventRef string `json:"raw_event_ref,omitempty"`
	// ExpectedStatus is the CI-strict Luna terminal status (ok, quarantined,
	// refused). Optional; defaults to quarantined for the invalid-input beat
	// and ok otherwise. Update after a live re-record if real model outcomes
	// differ from the stub bank so no-live AssertOperatorOK stays green.
	ExpectedStatus string `json:"expected_status,omitempty"`

	// Terra / Sol
	StateRevision int64             `json:"state_revision,omitempty"`
	Evidence      []EvidenceLiteral `json:"evidence,omitempty"`
	Note          string            `json:"note,omitempty"`

	// Sol
	Insights []InsightRef `json:"insights,omitempty"`
}

// EvidenceLiteral is an identity-bearing evidence pointer. Explanation text is
// part of Terra/Sol sameEvidence matching and must stay fixed.
type EvidenceLiteral struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Explanation string `json:"explanation"`
}

// InsightRef is the minimal Sol briefing insight citation.
type InsightRef struct {
	InsightID string `json:"insight_id"`
}

// LoadManifest reads and validates a recording manifest JSON file.
func LoadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest %s: %w", path, err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("validate manifest %s: %w", path, err)
	}
	return m, nil
}

// LoadDefaultManifest loads testdata/demo/recording-manifest.json under root.
func LoadDefaultManifest(repoRoot string) (Manifest, error) {
	return LoadManifest(filepath.Join(repoRoot, DefaultManifestRel))
}

// Validate checks structural requirements of the demo recording contract.
func (m Manifest) Validate() error {
	if strings.TrimSpace(m.Scenario) == "" {
		return fmt.Errorf("scenario is required")
	}
	if m.ExpectedCOPRevision < 1 {
		return fmt.Errorf("expected_cop_revision must be positive")
	}
	if strings.TrimSpace(m.SupervisorIdentity) == "" {
		return fmt.Errorf("supervisor_identity is required")
	}
	if len(m.Steps) == 0 {
		return fmt.Errorf("steps must not be empty")
	}
	// Play must be first so COP is at expected_cop_revision before model steps.
	if strings.ToLower(strings.TrimSpace(m.Steps[0].Kind)) != "play" {
		return fmt.Errorf("step 0 must be play (got %q); COP must advance before Luna/Terra/Sol", m.Steps[0].Kind)
	}
	var hasPlay, hasTerra, hasSol bool
	lunaCount := 0
	for i, step := range m.Steps {
		switch strings.ToLower(strings.TrimSpace(step.Kind)) {
		case "play":
			if i != 0 {
				return fmt.Errorf("step %d: play must appear only as the first step", i)
			}
			hasPlay = true
		case "luna":
			lunaCount++
			if strings.TrimSpace(step.BeatID) == "" {
				return fmt.Errorf("step %d: luna requires beat_id", i)
			}
			if strings.TrimSpace(step.RawEventRef) == "" {
				return fmt.Errorf("step %d: luna requires raw_event_ref", i)
			}
			if err := validateLunaExpectedStatus(step.ExpectedStatus); err != nil {
				return fmt.Errorf("step %d luna: %w", i, err)
			}
		case "terra":
			hasTerra = true
			if step.StateRevision < 1 {
				return fmt.Errorf("step %d: terra requires state_revision", i)
			}
			if step.StateRevision != m.ExpectedCOPRevision {
				return fmt.Errorf("step %d: terra state_revision %d must equal expected_cop_revision %d", i, step.StateRevision, m.ExpectedCOPRevision)
			}
			if len(step.Evidence) == 0 {
				return fmt.Errorf("step %d: terra requires evidence", i)
			}
			if err := validateEvidence(step.Evidence); err != nil {
				return fmt.Errorf("step %d terra: %w", i, err)
			}
		case "sol":
			hasSol = true
			if step.StateRevision < 1 {
				return fmt.Errorf("step %d: sol requires state_revision", i)
			}
			if step.StateRevision != m.ExpectedCOPRevision {
				return fmt.Errorf("step %d: sol state_revision %d must equal expected_cop_revision %d", i, step.StateRevision, m.ExpectedCOPRevision)
			}
			if len(step.Insights) == 0 {
				return fmt.Errorf("step %d: sol requires insights", i)
			}
			if len(step.Evidence) == 0 {
				return fmt.Errorf("step %d: sol requires evidence", i)
			}
			if err := validateEvidence(step.Evidence); err != nil {
				return fmt.Errorf("step %d sol: %w", i, err)
			}
			for j, ins := range step.Insights {
				if strings.TrimSpace(ins.InsightID) == "" {
					return fmt.Errorf("step %d sol insights[%d]: insight_id required", i, j)
				}
			}
		default:
			return fmt.Errorf("step %d: unknown kind %q", i, step.Kind)
		}
	}
	if !hasPlay {
		return fmt.Errorf("manifest must include a play step")
	}
	if lunaCount != 10 {
		return fmt.Errorf("manifest must include exactly 10 luna steps, got %d", lunaCount)
	}
	if !hasTerra {
		return fmt.Errorf("manifest must include a terra step")
	}
	if !hasSol {
		return fmt.Errorf("manifest must include a sol step")
	}
	return nil
}

func validateLunaExpectedStatus(status string) error {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		return nil
	}
	switch status {
	case "ok", "quarantined", "refused", "rejected":
		return nil
	default:
		return fmt.Errorf("expected_status %q is not a known Luna terminal (ok|quarantined|refused|rejected)", status)
	}
}

func validateEvidence(items []EvidenceLiteral) error {
	for i, item := range items {
		if strings.TrimSpace(item.Kind) == "" || strings.TrimSpace(item.ID) == "" {
			return fmt.Errorf("evidence[%d]: kind and id are required", i)
		}
		if strings.TrimSpace(item.Explanation) == "" {
			return fmt.Errorf("evidence[%d]: explanation is required (identity-bearing)", i)
		}
	}
	return nil
}

// CassetteDir returns the absolute cassette bank path under repoRoot.
func CassetteDir(repoRoot string) string {
	return filepath.Join(repoRoot, DefaultCassetteDirRel)
}
