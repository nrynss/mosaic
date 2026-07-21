package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
)

// Schema names for the COP materialization table (migration 0003).
const (
	// COPReadModelTable is the mutable system-of-record COP snapshot.
	COPReadModelTable = "cop_read_model"

	// COPReadModelColKey is the primary key (default contracts.DefaultCOPReadModelKey).
	COPReadModelColKey = "read_model_key"
)

// Compile-time proof that Store satisfies the read-model contract.
var _ contracts.COPReadModelRepository = (*Store)(nil)

// LoadCOPReadModel returns the active materialized COP for
// contracts.DefaultCOPReadModelKey. found is false when no row has been
// written yet (cold start / never projected); that is not an error.
func (s *Store) LoadCOPReadModel(ctx context.Context) (contracts.ProjectionResult, bool, error) {
	return s.LoadCOPReadModelKey(ctx, contracts.DefaultCOPReadModelKey)
}

// LoadCOPReadModelKey loads the materialization for an explicit key. Session
// isolation (C3) passes contracts.SessionCOPReadModelKey(sessionID).
func (s *Store) LoadCOPReadModelKey(ctx context.Context, key string) (contracts.ProjectionResult, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return contracts.ProjectionResult{}, false, fmt.Errorf("%w: cop read-model key is required", ErrInvalidRecord)
	}
	exec, err := s.executor(ctx)
	if err != nil {
		return contracts.ProjectionResult{}, false, err
	}

	var (
		stateRevision       int64
		projectedAt         time.Time
		copJSON             []byte
		checkpointJSON      []byte
		throughCanonicalSeq *int64
	)
	err = exec.QueryRow(ctx, `
		SELECT state_revision, projected_at, cop_json, checkpoint_json, through_canonical_seq
		FROM cop_read_model
		WHERE read_model_key = $1`, key,
	).Scan(&stateRevision, &projectedAt, &copJSON, &checkpointJSON, &throughCanonicalSeq)
	if errors.Is(err, pgx.ErrNoRows) {
		return contracts.ProjectionResult{}, false, nil
	}
	if err != nil {
		return contracts.ProjectionResult{}, false, fmt.Errorf("load cop read model %q: %w", key, err)
	}

	var cop map[string]any
	if err := json.Unmarshal(copJSON, &cop); err != nil {
		return contracts.ProjectionResult{}, false, fmt.Errorf("decode cop_json for %q: %w", key, err)
	}
	if cop == nil {
		cop = map[string]any{}
	}

	result := contracts.ProjectionResult{
		StateRevision: stateRevision,
		ProjectedAt:   projectedAt.UTC(),
		COP:           cop,
	}
	if len(checkpointJSON) > 0 {
		var checkpoint gen.Checkpoint
		if err := json.Unmarshal(checkpointJSON, &checkpoint); err != nil {
			return contracts.ProjectionResult{}, false, fmt.Errorf("decode checkpoint_json for %q: %w", key, err)
		}
		result.Checkpoint = checkpoint
	} else if throughCanonicalSeq != nil {
		result.Checkpoint = gen.Checkpoint{
			StateRevision:       stateRevision,
			ThroughCanonicalSeq: *throughCanonicalSeq,
		}
	}
	return result, true, nil
}

// SaveCOPReadModel UPSERTs the active materialization for
// contracts.DefaultCOPReadModelKey. When invoked under WithinTransaction it
// joins the ambient transaction (project+position+materialize atomicity).
func (s *Store) SaveCOPReadModel(ctx context.Context, result contracts.ProjectionResult) error {
	return s.SaveCOPReadModelKey(ctx, contracts.DefaultCOPReadModelKey, result)
}

// SaveCOPReadModelKey UPSERTs the materialization for an explicit key.
//
// # Revision CAS
//
// ON CONFLICT only applies the update when the incoming row is strictly newer:
// EXCLUDED.state_revision > stored.state_revision, or equal revision with a
// strictly greater through_canonical_seq (NULL treated as -1 for comparison).
// A concurrent or stale Save with a lower (or equal without progress) revision
// is a successful no-op so the higher materialization is never regressed.
func (s *Store) SaveCOPReadModelKey(ctx context.Context, key string, result contracts.ProjectionResult) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("%w: cop read-model key is required", ErrInvalidRecord)
	}
	if result.StateRevision < 0 {
		return fmt.Errorf("%w: state revision must be non-negative", ErrInvalidRecord)
	}
	// Normalize nil COP to empty object so materialization always stores valid JSONB.
	cop := result.COP
	if cop == nil {
		cop = map[string]any{}
	}

	copJSON, err := marshalRecord(cop)
	if err != nil {
		return fmt.Errorf("encode cop_json: %w", err)
	}

	var checkpointJSON any
	if strings.TrimSpace(result.Checkpoint.CheckpointID) != "" || result.Checkpoint.StateRevision > 0 || len(result.Checkpoint.COP) > 0 {
		encoded, err := marshalRecord(result.Checkpoint)
		if err != nil {
			return fmt.Errorf("encode checkpoint_json: %w", err)
		}
		checkpointJSON = encoded
	}

	var through any
	if result.Checkpoint.ThroughCanonicalSeq > 0 || result.Checkpoint.StateRevision > 0 {
		through = result.Checkpoint.ThroughCanonicalSeq
	}

	projectedAt := result.ProjectedAt
	if projectedAt.IsZero() {
		projectedAt = time.Now().UTC()
	} else {
		projectedAt = projectedAt.UTC()
	}

	exec, err := s.executor(ctx)
	if err != nil {
		return err
	}
	if _, err := exec.Exec(ctx, `
		INSERT INTO cop_read_model (
			read_model_key, state_revision, projected_at, cop_json,
			checkpoint_json, through_canonical_seq, updated_at
		) VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, $6, now())
		ON CONFLICT (read_model_key) DO UPDATE SET
			state_revision = EXCLUDED.state_revision,
			projected_at = EXCLUDED.projected_at,
			cop_json = EXCLUDED.cop_json,
			checkpoint_json = EXCLUDED.checkpoint_json,
			through_canonical_seq = EXCLUDED.through_canonical_seq,
			updated_at = now()
		WHERE EXCLUDED.state_revision > cop_read_model.state_revision
		   OR (
				EXCLUDED.state_revision = cop_read_model.state_revision
				AND COALESCE(EXCLUDED.through_canonical_seq, -1)
				  > COALESCE(cop_read_model.through_canonical_seq, -1)
		   )`,
		key, result.StateRevision, projectedAt, copJSON, checkpointJSON, through,
	); err != nil {
		return fmt.Errorf("save cop read model %q: %w", key, err)
	}
	return nil
}

// MaterializingProjector decorates a contracts.Projector so every successful
// ApplyCanonicalEvent / Replay also UPSERTs the COP read model.
//
// Domain projectors stay free of SQL; composition wires this decorator between
// the projector and the EventConsumer handle (or any other apply path).
//
// # Atomicity
//
// SaveCOPReadModel uses the ambient Store transaction when one is present.
// On the Postgres EventConsumer deliver path the outer WithinTransaction wraps
// handle (project + materialize) and the consumer checkpoint UPSERT, so
// project+position+materialize commit together. A Save failure rolls back the
// outer transaction and the event is redelivered.
//
// When Apply is invoked without an ambient transaction the inner domain
// projector may already have committed its own projection TX before Save runs.
// In that case Save is a separate transaction: a Save failure is returned to
// the caller (fail-closed for the request) but durable projection artifacts
// remain. Prefer wiring this decorator inside the consumer deliver TX.
type MaterializingProjector struct {
	Inner contracts.Projector
	COP   contracts.COPReadModelRepository
}

// Compile-time proof that MaterializingProjector remains a Projector.
var _ contracts.Projector = (*MaterializingProjector)(nil)

// NewMaterializingProjector wraps inner so successful projections are written
// to the COP read model. Both arguments are required.
func NewMaterializingProjector(inner contracts.Projector, cop contracts.COPReadModelRepository) *MaterializingProjector {
	if inner == nil {
		panic("pgstore: NewMaterializingProjector requires a non-nil Projector")
	}
	if cop == nil {
		panic("pgstore: NewMaterializingProjector requires a non-nil COPReadModelRepository")
	}
	return &MaterializingProjector{Inner: inner, COP: cop}
}

// ApplyCanonicalEvent projects via Inner then materializes the result.
func (m *MaterializingProjector) ApplyCanonicalEvent(ctx context.Context, event gen.CanonicalEvent) (contracts.ProjectionResult, error) {
	result, err := m.Inner.ApplyCanonicalEvent(ctx, event)
	if err != nil {
		return contracts.ProjectionResult{}, err
	}
	if err := m.COP.SaveCOPReadModel(ctx, result); err != nil {
		return contracts.ProjectionResult{}, fmt.Errorf("materialize COP after apply: %w", err)
	}
	return result, nil
}

// Replay rebuilds via Inner then materializes. Used when recovery falls back to
// a full canonical replay and composition wants the materialization warmed.
func (m *MaterializingProjector) Replay(ctx context.Context, checkpoint gen.Checkpoint, events []gen.CanonicalEvent) (contracts.ProjectionResult, error) {
	result, err := m.Inner.Replay(ctx, checkpoint, events)
	if err != nil {
		return contracts.ProjectionResult{}, err
	}
	// Empty rebuild (no checkpoint, no events) is a valid "no COP yet" result;
	// do not invent a materialization row for it.
	if result.StateRevision < 1 && len(result.COP) == 0 {
		return result, nil
	}
	if err := m.COP.SaveCOPReadModel(ctx, result); err != nil {
		return contracts.ProjectionResult{}, fmt.Errorf("materialize COP after replay: %w", err)
	}
	return result, nil
}
