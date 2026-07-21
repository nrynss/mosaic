package openaimodel

import (
	"encoding/json"
	"fmt"
	"strings"

	"mosaic.local/mosaic/internal/ontology/gen"
)

// evidenceRefWire is the authored evidence_ref shape used on Recommendation and
// Insight artifacts (target_kind, target_id, explanation, optional json_pointer).
// It deliberately omits full Evidence identity fields (evidence_id,
// schema_version, created_at) that models often echo from permitted-evidence
// request inputs.
type evidenceRefWire struct {
	TargetKind  string `json:"target_kind"`
	TargetID    string `json:"target_id"`
	JSONPointer string `json:"json_pointer,omitempty"`
	Explanation string `json:"explanation"`
}

// evidenceToWireRefs projects full Evidence records to evidence_ref items for
// model request payloads so the live path does not invite echo of identity
// fields that authored Recommendation/Insight schemas reject.
func evidenceToWireRefs(items []gen.Evidence) []evidenceRefWire {
	if len(items) == 0 {
		return nil
	}
	out := make([]evidenceRefWire, 0, len(items))
	for _, item := range items {
		ref := evidenceRefWire{
			TargetKind:  item.TargetKind,
			TargetID:    item.TargetID,
			Explanation: item.Explanation,
		}
		if pointer := strings.TrimSpace(item.JsonPointer); pointer != "" {
			ref.JSONPointer = pointer
		}
		out = append(out, ref)
	}
	return out
}

// normalizeArtifactEvidenceRefs rewrites root "evidence" arrays to the
// evidence_ref shape before Mosaic validates against authored schemas.
// Live models sometimes return full Evidence objects even under strict mode;
// stripping extras preserves semantic match checks (sameEvidence uses only
// target/explanation fields) while making schema validation pass.
func normalizeArtifactEvidenceRefs(document json.RawMessage) (json.RawMessage, error) {
	if len(document) == 0 {
		return nil, nil
	}
	var root map[string]any
	if err := json.Unmarshal(document, &root); err != nil {
		return nil, fmt.Errorf("decode artifact for evidence normalization: %w", err)
	}
	raw, ok := root["evidence"]
	if !ok {
		return append(json.RawMessage(nil), document...), nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return append(json.RawMessage(nil), document...), nil
	}
	normalized := make([]any, 0, len(arr))
	changed := false
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			normalized = append(normalized, item)
			continue
		}
		ref := map[string]any{}
		for _, key := range []string{"target_kind", "target_id", "explanation"} {
			if value, exists := m[key]; exists {
				ref[key] = value
			}
		}
		if value, exists := m["json_pointer"]; exists && value != nil {
			if s, ok := value.(string); !ok || strings.TrimSpace(s) != "" {
				ref["json_pointer"] = value
			}
		}
		if len(m) != len(ref) {
			changed = true
		} else {
			for key := range m {
				if _, keep := ref[key]; !keep {
					changed = true
					break
				}
			}
		}
		normalized = append(normalized, ref)
	}
	if !changed {
		return append(json.RawMessage(nil), document...), nil
	}
	root["evidence"] = normalized
	encoded, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("encode evidence-normalized artifact: %w", err)
	}
	return encoded, nil
}
