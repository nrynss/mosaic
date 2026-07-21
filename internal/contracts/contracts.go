// Package contracts contains stable seams between Mosaic parcels. Implementations
// belong to later packages; types here deliberately carry no runtime behaviour
// beyond shared sentinel errors that form part of a cross-package seam.
package contracts

import (
	"context"
	"errors"
	"time"

	"mosaic.local/mosaic/internal/ontology/gen"
)

// RawEventRepository preserves source envelopes before any model or projection work.
type RawEventRepository interface {
	AppendRawEvent(context.Context, gen.RawEvent) (RawEventAppendResult, error)
	FindRawEvent(ctx context.Context, rawEventID string) (gen.RawEvent, error)
}

// RawEventAppendResult makes idempotent re-delivery explicit without mutating history.
type RawEventAppendResult struct {
	RawEventID       string
	Created          bool
	ExistingResultID string
}

// CanonicalEventRepository owns the durable append order used for projection and replay.
type CanonicalEventRepository interface {
	AppendCanonicalEvent(context.Context, gen.CanonicalEvent) (gen.CanonicalEvent, error)
	ListCanonicalEventsAfter(ctx context.Context, canonicalSeq int64) ([]gen.CanonicalEvent, error)
	ListEffectiveCanonicalEventsForIncident(ctx context.Context, incidentID string) ([]gen.CanonicalEvent, error)
	MarkCanonicalEventProjected(ctx context.Context, canonicalEventID string, stateRevision int64) error
}

// ImmutableRecordRepository persists AI artifacts and auditable human actions separately.
type ImmutableRecordRepository interface {
	AppendLunaResult(context.Context, gen.LunaResult) error
	AppendInsight(context.Context, gen.Insight) error
	AppendRecommendation(context.Context, gen.Recommendation) error
	AppendModelRun(context.Context, gen.ModelRun) error
	AppendAuditRecord(context.Context, gen.AuditRecord) error
}

// AdvisoryHistoryReader returns persisted Terra/Sol advisory records for a bounded read model.
type AdvisoryHistoryReader interface {
	ReadAdvisoryHistory(context.Context) (AdvisoryHistory, error)
}

// AdvisoryHistory is an immutable advisory-domain snapshot without source payloads or commands.
type AdvisoryHistory struct {
	Insights        []gen.Insight
	Recommendations []gen.Recommendation
	ModelRuns       []gen.ModelRun
	AuditRecords    []gen.AuditRecord
}

// CheckpointRepository persists serializable COP checkpoints for deterministic recovery.
type CheckpointRepository interface {
	AppendCheckpoint(context.Context, gen.Checkpoint) error
	LatestCheckpoint(context.Context) (gen.Checkpoint, error)
}

// TransactionRunner establishes the narrow atomic boundary described in RFC-0001 §6.3.
type TransactionRunner interface {
	WithinTransaction(context.Context, func(context.Context) error) error
}

// Projector is the only interface permitted to mutate source-derived COP state.
type Projector interface {
	ApplyCanonicalEvent(context.Context, gen.CanonicalEvent) (ProjectionResult, error)
	Replay(context.Context, gen.Checkpoint, []gen.CanonicalEvent) (ProjectionResult, error)
}

// ProjectionResult is the serializable state output of a deterministic projection pass.
type ProjectionResult struct {
	StateRevision int64
	ProjectedAt   time.Time
	COP           map[string]any
	Checkpoint    gen.Checkpoint
}

// DefaultCOPReadModelKey is the active COP materialization key until session
// isolation (C3) introduces per-session epochs. Callers that do not yet scope
// by session always read and write this key.
const DefaultCOPReadModelKey = "default"

// COPReadModelRepository is the mutable system-of-record COP snapshot used for
// cheap GET /cop. It is separate from append-only checkpoints (recovery) and
// from the event log (transport). Implementations UPSERT one row per key;
// missing rows are reported as (zero, false, nil), not an error.
//
// Save is expected to join an ambient WithinTransaction when one is present so
// project+position+materialize can commit atomically on the Postgres consumer
// path. See pgstore.MaterializingProjector.
type COPReadModelRepository interface {
	LoadCOPReadModel(ctx context.Context) (ProjectionResult, bool, error)
	SaveCOPReadModel(ctx context.Context, result ProjectionResult) error
}

// ProjectorDispatcher schedules a committed canonical event for its deterministic projector.
type ProjectorDispatcher interface {
	DispatchCanonicalEvent(ctx context.Context, canonicalEventID string) error
}

// LunaAdapter is a structured normalizer. It cannot persist or project an event itself.
type LunaAdapter interface {
	Normalize(context.Context, gen.RawEvent) (LunaOutput, error)
}

// LunaOutput separates a status record from the optional projectable event.
type LunaOutput struct {
	Result         gen.LunaResult
	CanonicalEvent *gen.CanonicalEvent
	ModelRun       gen.ModelRun
}

// TerraAdapter creates assessments from a committed COP and resolvable evidence only.
type TerraAdapter interface {
	Assess(context.Context, TerraInput) (TerraOutput, error)
}

// TerraInput prohibits arbitrary raw source text in the assessment adapter.
type TerraInput struct {
	StateRevision int64
	COP           map[string]any
	Evidence      []gen.Evidence
}

// TerraOutput contains append-only assessment artifacts, never an operational command.
type TerraOutput struct {
	Insight  gen.Insight
	ModelRun gen.ModelRun
}

// SolAdapter produces a supervisor-requested, evidence-cited option without side effects.
type SolAdapter interface {
	Brief(context.Context, SolInput) (SolOutput, error)
}

// SolInput contains only structured state, insight, and evidence artifacts.
type SolInput struct {
	StateRevision int64
	COP           map[string]any
	Insights      []gen.Insight
	Evidence      []gen.Evidence
	RequestedBy   string
}

// SolOutput is stored for supervisor review and does not authorize an action.
type SolOutput struct {
	Recommendation gen.Recommendation
	ModelRun       gen.ModelRun
}

// ModelProvider represents the choice between fixture and live model responses.
type ModelProvider string

const (
	ProviderFixture ModelProvider = "fixture"
	ProviderLive    ModelProvider = "live"
)

// AgentProviderSelection maps agent keys (e.g., "luna", "terra", "sol") to their chosen ModelProvider.
type AgentProviderSelection map[string]ModelProvider

// SessionStatus represents the current lifecycle status of an interactive simulation session.
type SessionStatus string

const (
	SessionPending SessionStatus = "pending"
	SessionRunning SessionStatus = "running"
	SessionEnded   SessionStatus = "ended"
)

// ScheduledBeat represents a single event beat in the simulation schedule.
type ScheduledBeat struct {
	BeatID     string        `json:"beat_id"`
	Order      int           `json:"order"`
	RawEventID string        `json:"raw_event_id"`
	Delay      time.Duration `json:"delay"`
}

// SimulationSession represents the configuration and status of an interactive simulation session.
type SimulationSession struct {
	SessionID string          `json:"session_id"`
	Status    SessionStatus   `json:"status"`
	Beats     []ScheduledBeat `json:"beats"`
}

// SimulationSchedule exposes the ordered beats for a simulation session.
type SimulationSchedule interface {
	Beats() []ScheduledBeat
}

// StreamEventType represents the type of a simulation stream event.
type StreamEventType string

const (
	StreamEventBeat           StreamEventType = "beat"
	StreamEventStatusChange   StreamEventType = "status_change"
	StreamEventWorkspaceClear StreamEventType = "workspace_clear"
)

// SimulationStreamEvent represents a session-scoped stream event payload.
type SimulationStreamEvent struct {
	SessionID string          `json:"session_id"`
	Sequence  int64           `json:"sequence"`
	Timestamp time.Time       `json:"timestamp"`
	Type      StreamEventType `json:"type"`
	Payload   any             `json:"payload,omitempty"`
}

// ErrSimulationAlreadyRunning means Start was called while a session is already
// running. Framework adapters (e.g. the HTTP API) map this sentinel without
// importing the simulation package. Use Reset to begin a new session.
var ErrSimulationAlreadyRunning = errors.New("simulation session already running")

// SimulationStreamSubscription is a cancelable registration on a session-scoped
// stream. Implementations live under internal/simulation; consumers (including
// the HTTP adapter) depend only on this interface so framework packages never
// import simulation.
type SimulationStreamSubscription interface {
	// Events returns the receive-only channel of session stream events.
	Events() <-chan SimulationStreamEvent
	// Cancel unregisters the subscriber. Safe to call more than once.
	Cancel()
}
