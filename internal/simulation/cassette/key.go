package cassette

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/openaimodel"
	"mosaic.local/mosaic/internal/sol"
	"mosaic.local/mosaic/internal/terra"
)

const (
	agentTerra = "terra"
	agentSol   = "sol"
	agentLuna  = "luna"
	hashPrefix = 16 // hex characters used in the key path segment
)

// KeyMeta is optional simulation context mixed into the recording key.
// BeatID distinguishes multiple calls at the same revision when set.
type KeyMeta struct {
	BeatID string
}

// TerraKey returns the stable storage key and full request fingerprint hex
// for a Terra Assess request.
func TerraKey(req terra.Request, meta KeyMeta) (key string, fingerprint string, err error) {
	fp, err := terraFingerprint(req, meta)
	if err != nil {
		return "", "", err
	}
	full := sha256Hex(fp)
	return formatKey(agentTerra, req.StateRevision, meta.BeatID, full), full, nil
}

// SolKey returns the stable storage key and full request fingerprint hex
// for a Sol Brief request.
func SolKey(req sol.Request, meta KeyMeta) (key string, fingerprint string, err error) {
	fp, err := solFingerprint(req, meta)
	if err != nil {
		return "", "", err
	}
	full := sha256Hex(fp)
	return formatKey(agentSol, req.StateRevision, meta.BeatID, full), full, nil
}

// LunaKey returns the stable storage key and full request fingerprint hex for a
// Luna Normalize request. Luna is not COP-revision-scoped; keys are:
//
//	luna/{raw_event_id}[/{beat_id}]/{request_hash16}
//
// The fingerprint hashes the RawEventJSON bytes as presented plus the parsed
// raw_event_id when available.
func LunaKey(req openaimodel.LunaRequest, meta KeyMeta) (key string, fingerprint string, err error) {
	fp, rawEventID, err := lunaFingerprint(req, meta)
	if err != nil {
		return "", "", err
	}
	full := sha256Hex(fp)
	return formatLunaKey(rawEventID, meta.BeatID, full), full, nil
}

func formatKey(agent string, revision int64, beatID, fingerprintHex string) string {
	short := fingerprintHex
	if len(short) > hashPrefix {
		short = short[:hashPrefix]
	}
	beatID = strings.TrimSpace(beatID)
	if beatID == "" {
		return fmt.Sprintf("%s/rev%d/%s", agent, revision, short)
	}
	return fmt.Sprintf("%s/rev%d/%s/%s", agent, revision, sanitizeSegment(beatID), short)
}

func formatLunaKey(rawEventID, beatID, fingerprintHex string) string {
	short := fingerprintHex
	if len(short) > hashPrefix {
		short = short[:hashPrefix]
	}
	id := sanitizeSegment(rawEventID)
	if id == "" || id == "_" {
		id = "unknown"
	}
	beatID = strings.TrimSpace(beatID)
	if beatID == "" {
		return fmt.Sprintf("%s/%s/%s", agentLuna, id, short)
	}
	return fmt.Sprintf("%s/%s/%s/%s", agentLuna, id, sanitizeSegment(beatID), short)
}

func sanitizeSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "_"
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func terraFingerprint(req terra.Request, meta KeyMeta) ([]byte, error) {
	doc := map[string]any{
		"agent":          agentTerra,
		"state_revision": req.StateRevision,
		"cop_sha256":     sha256Hex(req.SerializedCOP),
		"evidence_ids":   sortedEvidenceIDs(req.Evidence),
	}
	if beat := strings.TrimSpace(meta.BeatID); beat != "" {
		doc["beat_id"] = beat
	}
	return json.Marshal(doc)
}

func solFingerprint(req sol.Request, meta KeyMeta) ([]byte, error) {
	doc := map[string]any{
		"agent":          agentSol,
		"state_revision": req.StateRevision,
		"cop_sha256":     sha256Hex(req.SerializedCOP),
		"evidence_ids":   sortedEvidenceIDs(req.Evidence),
		"insight_ids":    sortedInsightIDs(req.Insights),
		"requested_by":   req.RequestedBy,
	}
	if beat := strings.TrimSpace(meta.BeatID); beat != "" {
		doc["beat_id"] = beat
	}
	return json.Marshal(doc)
}

func lunaFingerprint(req openaimodel.LunaRequest, meta KeyMeta) (fp []byte, rawEventID string, err error) {
	rawEventID = extractRawEventID(req.RawEventJSON)
	doc := map[string]any{
		"agent":           agentLuna,
		"raw_event_id":    rawEventID,
		"raw_json_sha256": sha256Hex(req.RawEventJSON),
	}
	if beat := strings.TrimSpace(meta.BeatID); beat != "" {
		doc["beat_id"] = beat
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return nil, rawEventID, err
	}
	return encoded, rawEventID, nil
}

func extractRawEventID(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var envelope struct {
		RawEventID string `json:"raw_event_id"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ""
	}
	return strings.TrimSpace(envelope.RawEventID)
}

func sortedEvidenceIDs(evidence []gen.Evidence) []string {
	seen := make(map[string]struct{}, len(evidence))
	ids := make([]string, 0, len(evidence))
	for _, item := range evidence {
		id := strings.TrimSpace(item.EvidenceID)
		if id == "" {
			// Fall back to a stable structural key when EvidenceID is empty.
			id = evidenceFallbackKey(item)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func evidenceFallbackKey(e gen.Evidence) string {
	encoded, err := json.Marshal(struct {
		TargetKind  string `json:"target_kind"`
		TargetID    string `json:"target_id"`
		JSONPointer string `json:"json_pointer,omitempty"`
		Explanation string `json:"explanation"`
	}{
		TargetKind:  e.TargetKind,
		TargetID:    e.TargetID,
		JSONPointer: e.JsonPointer,
		Explanation: e.Explanation,
	})
	if err != nil {
		return fmt.Sprintf("%s|%s|%s", e.TargetKind, e.TargetID, e.JsonPointer)
	}
	return string(encoded)
}

func sortedInsightIDs(insights []gen.Insight) []string {
	seen := make(map[string]struct{}, len(insights))
	ids := make([]string, 0, len(insights))
	for _, item := range insights {
		id := strings.TrimSpace(item.InsightID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
