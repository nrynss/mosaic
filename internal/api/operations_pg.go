package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"mosaic.local/mosaic/internal/ontology/gen"
)

// PostgresOperationsReader is the SELECT-only operations adapter over pgstore.
// It produces the same OperationsSnapshot shape as SQLiteOperationsReader.
type PostgresOperationsReader struct {
	pool *pgxpool.Pool
}

// NewPostgresOperationsReader creates the Postgres operations read adapter.
func NewPostgresOperationsReader(pool *pgxpool.Pool) (*PostgresOperationsReader, error) {
	if pool == nil {
		return nil, errors.New("Postgres pool is required for operations telemetry")
	}
	return &PostgresOperationsReader{pool: pool}, nil
}

// ReadOperations uses only SELECT statements against append-only tables.
func (r *PostgresOperationsReader) ReadOperations(ctx context.Context) (OperationsSnapshot, error) {
	if r == nil || r.pool == nil {
		return OperationsSnapshot{}, errors.New("Postgres operations reader is not configured")
	}

	counts, err := r.counts(ctx)
	if err != nil {
		return OperationsSnapshot{}, err
	}
	latest, err := r.latestSourceReceivedAt(ctx)
	if err != nil {
		return OperationsSnapshot{}, err
	}
	lifecycle, err := r.lunaLifecycle(ctx)
	if err != nil {
		return OperationsSnapshot{}, err
	}
	modelRuns, err := r.modelRuns(ctx)
	if err != nil {
		return OperationsSnapshot{}, err
	}
	counts.LunaLifecycle = lifecycle
	counts.ModelRuns = modelRuns
	return OperationsSnapshot{LatestSourceReceivedAt: latest, Counts: counts}, nil
}

func (r *PostgresOperationsReader) counts(ctx context.Context) (OperationsCounts, error) {
	var counts OperationsCounts
	queries := []struct {
		name  string
		query string
		dest  *int
	}{
		{"raw events", "SELECT COUNT(*) FROM raw_events", &counts.RawEvents},
		{"canonical events", "SELECT COUNT(*) FROM canonical_events", &counts.CanonicalEvents},
		{"projected events", "SELECT COUNT(*) FROM canonical_projection_receipts", &counts.ProjectedEvents},
		{"unprojected events", `SELECT COUNT(*) FROM canonical_events AS canonical
			LEFT JOIN canonical_projection_receipts AS receipt
			ON receipt.canonical_event_id = canonical.canonical_event_id
			WHERE receipt.canonical_event_id IS NULL`, &counts.UnprojectedEvents},
		{"checkpoints", "SELECT COUNT(*) FROM checkpoints", &counts.Checkpoints},
		{"insights", "SELECT COUNT(*) FROM insights", &counts.Insights},
		{"recommendations", "SELECT COUNT(*) FROM recommendations", &counts.Recommendations},
		{"audit records", "SELECT COUNT(*) FROM audit_records", &counts.AuditRecords},
	}
	for _, item := range queries {
		if err := r.pool.QueryRow(ctx, item.query).Scan(item.dest); err != nil {
			return OperationsCounts{}, fmt.Errorf("count %s: %w", item.name, err)
		}
	}
	return counts, nil
}

func (r *PostgresOperationsReader) latestSourceReceivedAt(ctx context.Context) (*time.Time, error) {
	rows, err := r.pool.Query(ctx, "SELECT record_json FROM raw_events")
	if err != nil {
		return nil, fmt.Errorf("read raw event receipt times: %w", err)
	}
	defer rows.Close()

	var latest *time.Time
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return nil, fmt.Errorf("scan raw event receipt time: %w", err)
		}
		var raw gen.RawEvent
		if err := json.Unmarshal(encoded, &raw); err != nil {
			return nil, fmt.Errorf("decode raw event receipt time: %w", err)
		}
		if raw.ReceivedAt == "" {
			continue
		}
		receivedAt, err := time.Parse(time.RFC3339Nano, raw.ReceivedAt)
		if err != nil {
			return nil, fmt.Errorf("parse raw event receipt time: %w", err)
		}
		receivedAt = receivedAt.UTC()
		if latest == nil || receivedAt.After(*latest) {
			value := receivedAt
			latest = &value
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate raw event receipt times: %w", err)
	}
	return latest, nil
}

func (r *PostgresOperationsReader) lunaLifecycle(ctx context.Context) (LunaLifecycleCounts, error) {
	rows, err := r.pool.Query(ctx, "SELECT record_json FROM luna_results")
	if err != nil {
		return LunaLifecycleCounts{}, fmt.Errorf("read Luna lifecycle records: %w", err)
	}
	defer rows.Close()

	var counts LunaLifecycleCounts
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return LunaLifecycleCounts{}, fmt.Errorf("scan Luna lifecycle record: %w", err)
		}
		var result gen.LunaResult
		if err := json.Unmarshal(encoded, &result); err != nil {
			return LunaLifecycleCounts{}, fmt.Errorf("decode Luna lifecycle record: %w", err)
		}
		switch result.Status {
		case "accepted":
			counts.Accepted++
		case "repaired":
			counts.Repaired++
		case "quarantined":
			counts.Quarantined++
		case "rejected":
			counts.Rejected++
		default:
			return LunaLifecycleCounts{}, fmt.Errorf("unknown Luna lifecycle status %q", result.Status)
		}
	}
	if err := rows.Err(); err != nil {
		return LunaLifecycleCounts{}, fmt.Errorf("iterate Luna lifecycle records: %w", err)
	}
	return counts, nil
}

func (r *PostgresOperationsReader) modelRuns(ctx context.Context) (ModelRunCounts, error) {
	rows, err := r.pool.Query(ctx, "SELECT record_json FROM model_runs")
	if err != nil {
		return ModelRunCounts{}, fmt.Errorf("read model runs: %w", err)
	}
	defer rows.Close()

	byAgent := map[string]ValidationStatusCounts{
		"luna":  {},
		"sol":   {},
		"terra": {},
	}
	var total ValidationStatusCounts
	var count int
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return ModelRunCounts{}, fmt.Errorf("scan model run: %w", err)
		}
		var run gen.ModelRun
		if err := json.Unmarshal(encoded, &run); err != nil {
			return ModelRunCounts{}, fmt.Errorf("decode model run: %w", err)
		}
		agent, knownAgent := byAgent[run.Agent]
		if !knownAgent {
			return ModelRunCounts{}, fmt.Errorf("unknown model-run agent %q", run.Agent)
		}
		if err := incrementValidationStatus(&agent, run.ValidationStatus); err != nil {
			return ModelRunCounts{}, err
		}
		if err := incrementValidationStatus(&total, run.ValidationStatus); err != nil {
			return ModelRunCounts{}, err
		}
		byAgent[run.Agent] = agent
		count++
	}
	if err := rows.Err(); err != nil {
		return ModelRunCounts{}, fmt.Errorf("iterate model runs: %w", err)
	}

	agents := []string{"luna", "sol", "terra"}
	sort.Strings(agents)
	grouped := make([]ModelRunAgentCounts, 0, len(agents))
	for _, agent := range agents {
		statuses := byAgent[agent]
		grouped = append(grouped, ModelRunAgentCounts{
			Agent:              agent,
			Total:              validationStatusTotal(statuses),
			ValidationStatuses: statuses,
		})
	}
	return ModelRunCounts{Total: count, ByAgent: grouped, ValidationStatuses: total}, nil
}

var _ OperationsReader = (*PostgresOperationsReader)(nil)
