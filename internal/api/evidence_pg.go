package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresEvidenceResolver is the SELECT-only evidence adapter over pgstore's
// append-only tables. It mirrors SQLiteEvidenceResolver without depending on
// database/sql placeholders.
type PostgresEvidenceResolver struct {
	pool       *pgxpool.Pool
	stateFacts StateFactResolver
}

// NewPostgresEvidenceResolver creates a read-only resolver over a Postgres pool.
// A selected domain may optionally supply a StateFactResolver.
func NewPostgresEvidenceResolver(pool *pgxpool.Pool, stateFacts ...StateFactResolver) (*PostgresEvidenceResolver, error) {
	if pool == nil {
		return nil, errors.New("Postgres pool is required for evidence resolution")
	}
	if len(stateFacts) > 1 {
		return nil, errors.New("at most one state-fact resolver may be configured")
	}
	resolver := &PostgresEvidenceResolver{pool: pool}
	if len(stateFacts) == 1 {
		resolver.stateFacts = stateFacts[0]
	}
	return resolver, nil
}

// Resolve finds one evidence target. Supported kinds match SQLiteEvidenceResolver.
func (r *PostgresEvidenceResolver) Resolve(ctx context.Context, kind, id string, cop map[string]any) (Resolution, error) {
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

func (r *PostgresEvidenceResolver) resolveStored(ctx context.Context, resolution Resolution) (Resolution, error) {
	if r == nil || r.pool == nil {
		return Resolution{}, errors.New("Postgres evidence resolver is not configured")
	}
	table, idColumn, ok := artifactTable(resolution.Kind)
	if !ok {
		resolution.Reason = fmt.Sprintf("unsupported artifact kind %q", resolution.Kind)
		return resolution, nil
	}

	query := fmt.Sprintf("SELECT record_json FROM %s WHERE %s = $1", table, idColumn)
	var encoded []byte
	err := r.pool.QueryRow(ctx, query, resolution.ID).Scan(&encoded)
	if errors.Is(err, pgx.ErrNoRows) {
		resolution.Reason = "artifact was not found in the append-only store"
		return resolution, nil
	}
	if err != nil {
		return Resolution{}, fmt.Errorf("read %s %q: %w", resolution.Kind, resolution.ID, err)
	}

	var artifact any
	if err := json.Unmarshal(encoded, &artifact); err != nil {
		return Resolution{}, fmt.Errorf("decode %s %q: %w", resolution.Kind, resolution.ID, err)
	}
	resolution.Resolved = true
	resolution.Artifact = artifact
	return resolution, nil
}

var _ EvidenceResolver = (*PostgresEvidenceResolver)(nil)
