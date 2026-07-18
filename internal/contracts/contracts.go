// Package contracts contains stable seams between Mosaic parcels. Implementations
// belong to later packages; types here deliberately carry no runtime behaviour.
package contracts

import (
	"context"
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
