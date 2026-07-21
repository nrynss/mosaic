package cassette

import (
	"encoding/json"
	"errors"
	"time"
)

// SchemaVersion tags the on-disk recording envelope. Bump only for breaking
// layout changes; additive optional fields stay on the same version.
const SchemaVersion = "1.0.0"

// ErrReplayMiss means ModeReplay found no recording for the request key.
// Callers must not fall back to a live client call.
var ErrReplayMiss = errors.New("cassette: no recording for key")

// ErrInnerRequired means Record or Passthrough mode needs a non-nil Inner client.
var ErrInnerRequired = errors.New("cassette: inner StructuredClient is required")

// ErrStoreRequired means Record or Replay mode needs a non-nil Store.
var ErrStoreRequired = errors.New("cassette: store is required")

// Recording is the persisted envelope for one Terra, Sol, or Luna structured
// client call.
//
// PromptVersion and PromptHash bank the H1 prompt provenance used when the
// response was recorded (split from "v1.0.0+sha256:<hex>"). ModeRecord copies
// them from the decorator; ModeReplay restores them onto the decorator and
// compose may rejoin them into ModelRun.PromptVersion via JoinPromptProvenance.
// Older recordings may leave both empty.
//
// Agent-specific payload fields are mutually exclusive by agent:
// Terra → InsightJSON, Sol → RecommendationJSON, Luna → ResultJSON (+ optional
// CanonicalEventJSON).
type Recording struct {
	SchemaVersion      string          `json:"schema_version"`
	Key                string          `json:"key"`
	Agent              string          `json:"agent"`
	StateRevision      int64           `json:"state_revision"`
	BeatID             string          `json:"beat_id,omitempty"`
	RequestFingerprint string          `json:"request_fingerprint"`
	PromptVersion      string          `json:"prompt_version,omitempty"`
	PromptHash         string          `json:"prompt_hash,omitempty"`
	ResponseID         string          `json:"response_id,omitempty"`
	RefusalDetail      string          `json:"refusal_detail,omitempty"`
	InsightJSON        json.RawMessage `json:"insight_json,omitempty"`
	RecommendationJSON json.RawMessage `json:"recommendation_json,omitempty"`
	// ResultJSON / CanonicalEventJSON are Luna Normalize outputs.
	ResultJSON         json.RawMessage `json:"result_json,omitempty"`
	CanonicalEventJSON json.RawMessage `json:"canonical_event_json,omitempty"`
	RecordedAt         string          `json:"recorded_at,omitempty"`
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
