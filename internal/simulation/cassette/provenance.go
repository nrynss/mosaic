package cassette

import "strings"

// Agent names stored on Recording.Agent (stable key / provenance filter).
const (
	AgentTerra = agentTerra
	AgentSol   = agentSol
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

// BankedPromptProvenance returns JoinPromptProvenance for the first recording
// whose Agent matches agent (case-sensitive) and whose PromptVersion is
// non-empty. Returns empty when no banked provenance is present.
//
// Used at compose time under ModeReplay so ModelRun.PromptVersion can cite the
// recorded prompt instead of a generic opaque id.
func BankedPromptProvenance(recs []*Recording, agent string) string {
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return ""
	}
	for _, rec := range recs {
		if rec == nil {
			continue
		}
		if strings.TrimSpace(rec.Agent) != agent {
			continue
		}
		if p := JoinPromptProvenance(rec.PromptVersion, rec.PromptHash); p != "" {
			return p
		}
	}
	return ""
}
