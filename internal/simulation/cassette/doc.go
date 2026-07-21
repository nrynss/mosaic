// Package cassette provides record/replay decorators around Terra and Sol
// StructuredClient interfaces for the interactive simulation modes.
//
// # Modes
//
//   - Passthrough: call the inner client only (fixture path can ignore cassette).
//   - Record: call the inner client, persist request fingerprint + response, return it.
//   - Replay: look up by key and return the recorded Response with no network call.
//
// Missing replay entries return a clear error (never a silent live call).
//
// # Key algorithm
//
// Each recording is addressed by a stable string key:
//
//	{agent}/rev{state_revision}[/{beat_id}]/{request_hash16}
//
// where:
//
//   - agent is "terra" or "sol"
//   - state_revision is the committed COP revision on the request
//   - beat_id is optional simulation context (empty when not set)
//   - request_hash16 is the first 16 hex characters of SHA-256 over a
//     canonical JSON fingerprint of the request identity
//
// Terra fingerprint fields (sorted map → JSON):
//
//	agent, state_revision, beat_id (if non-empty),
//	cop_sha256 (SHA-256 hex of SerializedCOP bytes as presented),
//	evidence_ids (sorted unique EvidenceID values)
//
// Sol fingerprint fields:
//
//	agent, state_revision, beat_id (if non-empty),
//	cop_sha256, evidence_ids (sorted unique),
//	insight_ids (sorted unique InsightID values),
//	requested_by
//
// The full fingerprint SHA-256 hex is also stored on the recording as
// request_fingerprint for collision diagnostics. Keys are stable for identical
// request identity inputs; changing COP bytes, evidence set, insights (Sol),
// beat_id, or revision produces a different key.
//
// # Prompt provenance
//
// Recording entries carry optional prompt_version and prompt_hash fields.
// Composition sets them on the decorator from the loaded H1 provenance string
// ("v1.0.0+sha256:<hex>") before ModeRecord; the decorator copies them onto
// each new recording. ModeReplay restores them onto the decorator after Get.
// JoinPromptProvenance / BankedPromptProvenance rejoin banked fields for
// ModelRun.PromptVersion under replay. The cassette does not load or hash
// prompts itself — that remains composition's responsibility.
//
// Demo invariant: a bank is one live-record session with a uniform prompt per
// agent. ModelRun.PromptVersion is resolved once at compose (not per Assess).
// BankedPromptProvenance picks the newest RecordedAt when a bank mixes versions.
//
// # Storage
//
// MemoryStore is for tests. FileStore writes one JSON file per recording under
// a configurable directory (suitable for a local recordings/ path that is
// gitignored, or a test temp dir). Banked live runs can later be committed if
// desired — entries must remain synthetic / non-secret.
//
// # Dependency direction
//
// This package lives under internal/simulation and may import terra and sol
// interfaces. Framework packages must not import cassette.
package cassette
