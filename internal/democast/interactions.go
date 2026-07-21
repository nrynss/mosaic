package democast

import (
	"fmt"
	"strings"
)

// InteractionsDocument is the browser-safe, ready-to-POST view of the recording
// manifest. It carries no secrets and no raw wire dumps beyond the synthetic
// dataset fields already used by operator/interpret.
type InteractionsDocument struct {
	Scenario            string            `json:"scenario"`
	ExpectedCOPRevision int64             `json:"expected_cop_revision"`
	SupervisorIdentity  string            `json:"supervisor_identity"`
	Steps               []InteractionStep `json:"steps"`
}

// InteractionStep is one operator model action the demo UI may issue. Play
// steps are omitted — the UI drives simulation separately.
type InteractionStep struct {
	Kind           string         `json:"kind"` // luna | terra | sol
	BeatID         string         `json:"beat_id,omitempty"`
	RawEventRef    string         `json:"raw_event_ref,omitempty"`
	ExpectedStatus string         `json:"expected_status,omitempty"`
	StateRevision  int64          `json:"state_revision,omitempty"`
	Endpoint       string         `json:"endpoint"` // relative to /api/v1/
	Request        map[string]any `json:"request"`
}

// BuildInteractions loads the default recording manifest and dataset raw events
// under assetRoot and returns ready-to-POST operator payloads. Fingerprints
// match democast.Driver so replay hits the cassette bank.
func BuildInteractions(assetRoot string) (InteractionsDocument, error) {
	assetRoot = strings.TrimSpace(assetRoot)
	if assetRoot == "" {
		return InteractionsDocument{}, fmt.Errorf("asset root is required")
	}
	m, err := LoadDefaultManifest(assetRoot)
	if err != nil {
		return InteractionsDocument{}, err
	}
	raw, err := LoadRawEvents(assetRoot, m.Scenario)
	if err != nil {
		return InteractionsDocument{}, err
	}
	return BuildInteractionsFrom(m, raw)
}

// BuildInteractionsFrom projects an already-loaded manifest + raw index into
// the browser-safe interactions document.
func BuildInteractionsFrom(m Manifest, raw RawEventIndex) (InteractionsDocument, error) {
	if raw == nil {
		return InteractionsDocument{}, fmt.Errorf("raw event index is required")
	}
	doc := InteractionsDocument{
		Scenario:            m.Scenario,
		ExpectedCOPRevision: m.ExpectedCOPRevision,
		SupervisorIdentity:  m.SupervisorIdentity,
		Steps:               make([]InteractionStep, 0, len(m.Steps)),
	}
	for i, step := range m.Steps {
		kind := strings.ToLower(strings.TrimSpace(step.Kind))
		switch kind {
		case "play":
			continue
		case "luna":
			ev, err := raw.Get(step.RawEventRef)
			if err != nil {
				return InteractionsDocument{}, fmt.Errorf("step %d luna: %w", i, err)
			}
			doc.Steps = append(doc.Steps, InteractionStep{
				Kind:           "luna",
				BeatID:         step.BeatID,
				RawEventRef:    step.RawEventRef,
				ExpectedStatus: strings.TrimSpace(step.ExpectedStatus),
				Endpoint:       "operator/interpret",
				Request:        interpretBodyFromRaw(ev),
			})
		case "terra":
			doc.Steps = append(doc.Steps, InteractionStep{
				Kind:          "terra",
				StateRevision: step.StateRevision,
				Endpoint:      "operator/analyze",
				Request: map[string]any{
					"evidence": evidencePayload(step.Evidence),
					"note":     step.Note,
				},
			})
		case "sol":
			insights := make([]map[string]any, 0, len(step.Insights))
			for _, ins := range step.Insights {
				insights = append(insights, map[string]any{"insight_id": ins.InsightID})
			}
			doc.Steps = append(doc.Steps, InteractionStep{
				Kind:          "sol",
				StateRevision: step.StateRevision,
				Endpoint:      "operator/brief",
				Request: map[string]any{
					"insights": insights,
					"evidence": evidencePayload(step.Evidence),
					"note":     step.Note,
				},
			})
		default:
			return InteractionsDocument{}, fmt.Errorf("step %d: unknown kind %q", i, step.Kind)
		}
	}
	if len(doc.Steps) == 0 {
		return InteractionsDocument{}, fmt.Errorf("manifest has no operator model steps")
	}
	return doc, nil
}
