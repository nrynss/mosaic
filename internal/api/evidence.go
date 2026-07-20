package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"mosaic.local/mosaic/internal/store"
)

// Resolution is the explicit result of resolving an evidence target or a
// stored immutable artifact. Missing targets are represented as Resolved false
// rather than as an ambiguous empty JSON object.
type Resolution struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Resolved bool   `json:"resolved"`
	Artifact any    `json:"artifact,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// EvidenceResolver resolves only persisted artifacts and the supplied COP.
// It is an API-local seam because P02's repository contracts intentionally
// cover writes and deterministic replay, not ad hoc read-model queries.
type EvidenceResolver interface {
	Resolve(context.Context, string, string, map[string]any) (Resolution, error)
}

// SQLiteEvidenceResolver is the P08 read adapter over P03's SQLite store. It
// performs SELECT-only queries through Store.SQLDB and does not extend or alter
// any P03 persistence contract.
type SQLiteEvidenceResolver struct {
	db         *sql.DB
	stateFacts StateFactResolver
}

// NewSQLiteEvidenceResolver creates a read-only resolver for a P03 store.
// A selected domain may optionally supply a StateFactResolver. Without one,
// state_fact evidence remains explicitly unavailable while persisted artifact
// resolution continues to work.
func NewSQLiteEvidenceResolver(source *store.Store, stateFacts ...StateFactResolver) (*SQLiteEvidenceResolver, error) {
	if source == nil || source.SQLDB() == nil {
		return nil, errors.New("SQLite store is required for evidence resolution")
	}
	if len(stateFacts) > 1 {
		return nil, errors.New("at most one state-fact resolver may be configured")
	}
	resolver := &SQLiteEvidenceResolver{db: source.SQLDB()}
	if len(stateFacts) == 1 {
		resolver.stateFacts = stateFacts[0]
	}
	return resolver, nil
}

// Resolve finds one evidence target. Supported evidence target kinds are the
// four values defined by ontology/evidence.schema.json. The returned result is
// explicit when a target cannot be resolved.
func (r *SQLiteEvidenceResolver) Resolve(ctx context.Context, kind, id string, cop map[string]any) (Resolution, error) {
	resolution := Resolution{Kind: kind, ID: id}
	if strings.TrimSpace(id) == "" {
		resolution.Reason = "an artifact ID is required"
		return resolution, nil
	}

	switch kind {
	case "state_fact":
		if r.stateFacts == nil {
			resolution.Reason = "state_fact evidence is unavailable for this composition"
			return resolution, nil
		}
		return r.stateFacts.ResolveStateFact(ctx, id, cop)
	case "raw_event", "canonical_event", "luna_result", "insight", "recommendation", "model_run", "audit_record", "checkpoint":
		return r.resolveStored(ctx, resolution)
	default:
		resolution.Reason = fmt.Sprintf("unsupported artifact kind %q", kind)
		return resolution, nil
	}
}

func (r *SQLiteEvidenceResolver) resolveStored(ctx context.Context, resolution Resolution) (Resolution, error) {
	if r == nil || r.db == nil {
		return Resolution{}, errors.New("SQLite evidence resolver is not configured")
	}
	table, idColumn, ok := artifactTable(resolution.Kind)
	if !ok {
		resolution.Reason = fmt.Sprintf("unsupported artifact kind %q", resolution.Kind)
		return resolution, nil
	}

	query := fmt.Sprintf("SELECT record_json FROM %s WHERE %s = ?", table, idColumn)
	var encoded string
	err := r.db.QueryRowContext(ctx, query, resolution.ID).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		resolution.Reason = "artifact was not found in the append-only store"
		return resolution, nil
	}
	if err != nil {
		return Resolution{}, fmt.Errorf("read %s %q: %w", resolution.Kind, resolution.ID, err)
	}

	var artifact any
	if err := json.Unmarshal([]byte(encoded), &artifact); err != nil {
		return Resolution{}, fmt.Errorf("decode %s %q: %w", resolution.Kind, resolution.ID, err)
	}
	resolution.Resolved = true
	resolution.Artifact = artifact
	return resolution, nil
}

func artifactTable(kind string) (table, idColumn string, ok bool) {
	switch kind {
	case "raw_event":
		return "raw_events", "raw_event_id", true
	case "canonical_event":
		return "canonical_events", "canonical_event_id", true
	case "luna_result":
		return "luna_results", "luna_result_id", true
	case "insight":
		return "insights", "insight_id", true
	case "recommendation":
		return "recommendations", "recommendation_id", true
	case "model_run":
		return "model_runs", "model_run_id", true
	case "audit_record":
		return "audit_records", "audit_record_id", true
	case "checkpoint":
		return "checkpoints", "checkpoint_id", true
	default:
		return "", "", false
	}
}

// StateFactResolver interprets state_fact evidence for one selected domain.
// The generic API owns stored-record resolution only; a domain profile owns
// the shape and identifiers of its deterministic COP facts.
type StateFactResolver interface {
	ResolveStateFact(context.Context, string, map[string]any) (Resolution, error)
}
