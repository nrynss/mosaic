package cassette

import (
	"strings"
	"time"
)

// Agent names stored on Recording.Agent (stable key / provenance filter).
const (
	AgentTerra = agentTerra
	AgentSol   = agentSol
	AgentLuna  = agentLuna
)

// JoinPromptProvenance rebuilds H1-style provenance "version+sha256:hash".
// Empty version yields empty string; empty hash returns version alone.
func JoinPromptProvenance(version, hash string) string {
	version = strings.TrimSpace(version)
	hash = strings.TrimSpace(hash)
	if version == "" {
		return ""
	}
	if hash == "" {
		return version
	}
	return version + "+sha256:" + hash
}

// BankedPromptProvenance returns JoinPromptProvenance for the most recently
// recorded matching agent entry (by RecordedAt, then Key for ties). Agent match
// is case-sensitive. Returns empty when no banked provenance is present.
//
// Demo invariant: a bank is normally one live-record session with a uniform
// prompt; ModelRun.PromptVersion is still compose-time (not per-Assess). When
// a bank mixes prompts for the same agent, the newest RecordedAt wins.
//
// Used at compose time under ModeReplay so ModelRun.PromptVersion can cite the
// recorded prompt instead of a generic opaque id.
func BankedPromptProvenance(recs []*Recording, agent string) string {
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return ""
	}
	var best *Recording
	var bestTime time.Time
	var bestHasTime bool
	for _, rec := range recs {
		if rec == nil {
			continue
		}
		if strings.TrimSpace(rec.Agent) != agent {
			continue
		}
		if JoinPromptProvenance(rec.PromptVersion, rec.PromptHash) == "" {
			continue
		}
		if best == nil {
			best = rec
			bestTime, bestHasTime = parseRecordedAt(rec.RecordedAt)
			continue
		}
		t, ok := parseRecordedAt(rec.RecordedAt)
		switch {
		case ok && bestHasTime:
			if t.After(bestTime) || (t.Equal(bestTime) && rec.Key > best.Key) {
				best, bestTime, bestHasTime = rec, t, true
			}
		case ok && !bestHasTime:
			best, bestTime, bestHasTime = rec, t, true
		case !ok && !bestHasTime:
			if rec.Key > best.Key {
				best = rec
			}
		}
		// Prefer timed over untimed already handled; untimed never beats timed.
	}
	if best == nil {
		return ""
	}
	return JoinPromptProvenance(best.PromptVersion, best.PromptHash)
}

func parseRecordedAt(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	// Recordings use RFC3339Nano; accept RFC3339 as a fallback.
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, true
	}
	return time.Time{}, false
}
