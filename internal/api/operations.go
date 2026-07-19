package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"mosaic.local/mosaic/internal/ontology/gen"
	"mosaic.local/mosaic/internal/store"
	"mosaic.local/mosaic/internal/stream"
)

// OperationsReader returns bounded, typed operational facts for the public
// demo read model. It is intentionally API-local: a future shared-store
// adapter is selected by composition without enlarging durable repository
// contracts for a dashboard query.
type OperationsReader interface {
	ReadOperations(context.Context) (OperationsSnapshot, error)
}

// OperationsSnapshot contains durable facts only. The HTTP layer combines it
// with a same-request deterministic recovery result and process-local stream
// metadata before returning an operations response.
type OperationsSnapshot struct {
	LatestSourceReceivedAt *time.Time       `json:"latest_source_received_at,omitempty"`
	Counts                 OperationsCounts `json:"counts"`
}

// OperationsCounts contains only bounded counts derived from immutable rows
// and projection receipts. It never exposes record payloads.
type OperationsCounts struct {
	RawEvents         int                 `json:"raw_events"`
	CanonicalEvents   int                 `json:"canonical_events"`
	ProjectedEvents   int                 `json:"projected_events"`
	UnprojectedEvents int                 `json:"unprojected_events"`
	Checkpoints       int                 `json:"checkpoints"`
	Insights          int                 `json:"insights"`
	Recommendations   int                 `json:"recommendations"`
	AuditRecords      int                 `json:"audit_records"`
	LunaLifecycle     LunaLifecycleCounts `json:"luna_lifecycle"`
	ModelRuns         ModelRunCounts      `json:"model_runs"`
}

// LunaLifecycleCounts enumerates the versioned Luna lifecycle outcomes.
type LunaLifecycleCounts struct {
	Accepted    int `json:"accepted"`
	Repaired    int `json:"repaired"`
	Quarantined int `json:"quarantined"`
	Rejected    int `json:"rejected"`
}

// ValidationStatusCounts enumerates the versioned ModelRun validation states.
type ValidationStatusCounts struct {
	Valid    int `json:"valid"`
	Invalid  int `json:"invalid"`
	Refused  int `json:"refused"`
	Failed   int `json:"failed"`
	TimedOut int `json:"timed_out"`
}

// ModelRunAgentCounts is a stable, ordered grouping for one known agent.
type ModelRunAgentCounts struct {
	Agent              string                 `json:"agent"`
	Total              int                    `json:"total"`
	ValidationStatuses ValidationStatusCounts `json:"validation_statuses"`
}

// ModelRunCounts groups immutable invocation outcomes globally and per agent.
type ModelRunCounts struct {
	Total              int                    `json:"total"`
	ByAgent            []ModelRunAgentCounts  `json:"by_agent"`
	ValidationStatuses ValidationStatusCounts `json:"validation_statuses"`
}

// SQLiteOperationsReader is the local, SELECT-only P17 adapter over P03's
// SQLite store. It reads bounded aggregates and typed timestamps, never raw
// event payloads, checksums, prompts, or model responses.
type SQLiteOperationsReader struct {
	db *sql.DB
}

// NewSQLiteOperationsReader creates the local operations read adapter.
func NewSQLiteOperationsReader(source *store.Store) (*SQLiteOperationsReader, error) {
	if source == nil || source.SQLDB() == nil {
		return nil, errors.New("SQLite store is required for operations telemetry")
	}
	return &SQLiteOperationsReader{db: source.SQLDB()}, nil
}

// ReadOperations uses only SELECT statements against append-only tables. It
// produces zero-valued known lifecycle and model statuses when no matching
// record has been stored, so the result shape remains deterministic.
func (r *SQLiteOperationsReader) ReadOperations(ctx context.Context) (OperationsSnapshot, error) {
	if r == nil || r.db == nil {
		return OperationsSnapshot{}, errors.New("SQLite operations reader is not configured")
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

func (r *SQLiteOperationsReader) counts(ctx context.Context) (OperationsCounts, error) {
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
		if err := r.db.QueryRowContext(ctx, item.query).Scan(item.dest); err != nil {
			return OperationsCounts{}, fmt.Errorf("count %s: %w", item.name, err)
		}
	}
	return counts, nil
}

func (r *SQLiteOperationsReader) latestSourceReceivedAt(ctx context.Context) (*time.Time, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT record_json FROM raw_events")
	if err != nil {
		return nil, fmt.Errorf("read raw event receipt times: %w", err)
	}
	defer rows.Close()

	var latest *time.Time
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return nil, fmt.Errorf("scan raw event receipt time: %w", err)
		}
		var raw gen.RawEvent
		if err := json.Unmarshal([]byte(encoded), &raw); err != nil {
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

func (r *SQLiteOperationsReader) lunaLifecycle(ctx context.Context) (LunaLifecycleCounts, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT record_json FROM luna_results")
	if err != nil {
		return LunaLifecycleCounts{}, fmt.Errorf("read Luna lifecycle records: %w", err)
	}
	defer rows.Close()

	var counts LunaLifecycleCounts
	for rows.Next() {
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return LunaLifecycleCounts{}, fmt.Errorf("scan Luna lifecycle record: %w", err)
		}
		var result gen.LunaResult
		if err := json.Unmarshal([]byte(encoded), &result); err != nil {
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

func (r *SQLiteOperationsReader) modelRuns(ctx context.Context) (ModelRunCounts, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT record_json FROM model_runs")
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
		var encoded string
		if err := rows.Scan(&encoded); err != nil {
			return ModelRunCounts{}, fmt.Errorf("scan model run: %w", err)
		}
		var run gen.ModelRun
		if err := json.Unmarshal([]byte(encoded), &run); err != nil {
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

func incrementValidationStatus(counts *ValidationStatusCounts, status string) error {
	switch status {
	case "valid":
		counts.Valid++
	case "invalid":
		counts.Invalid++
	case "refused":
		counts.Refused++
	case "failed":
		counts.Failed++
	case "timed_out":
		counts.TimedOut++
	default:
		return fmt.Errorf("unknown model-run validation status %q", status)
	}
	return nil
}

func validationStatusTotal(counts ValidationStatusCounts) int {
	return counts.Valid + counts.Invalid + counts.Refused + counts.Failed + counts.TimedOut
}

// operationsResponse is the bounded public /api/v1/operations contract. It
// intentionally contains no raw payload, checksum, model prompt, response, or
// other immutable record body.
type operationsResponse struct {
	ObservedAt             time.Time                    `json:"observed_at"`
	LatestSourceReceivedAt *time.Time                   `json:"latest_source_received_at,omitempty"`
	Service                operationsService            `json:"service"`
	Recovery               operationsRecovery           `json:"recovery"`
	Counts                 OperationsCounts             `json:"counts"`
	Stream                 operationsStream             `json:"stream"`
	Capabilities           []operationsCapabilityStatus `json:"capabilities"`
}

type operationsService struct {
	Version       string    `json:"version"`
	StartedAt     time.Time `json:"started_at"`
	UptimeSeconds int64     `json:"uptime_seconds"`
}

type operationsRecovery struct {
	Status        string    `json:"status"`
	StateRevision int64     `json:"state_revision"`
	ProjectedAt   time.Time `json:"projected_at"`
}

type operationsStream struct {
	LocalSubscriberCount int                 `json:"local_subscriber_count"`
	LastPublished        *stream.Publication `json:"last_published,omitempty"`
}

// operationsCapabilityStatus is a static statement of a bounded demo feature,
// its composition mode, and its present status. It is deliberately not a
// generic monitoring claim and never describes reconciliation as self-healing.
type operationsCapabilityStatus struct {
	Capability string `json:"capability"`
	Feature    string `json:"feature"`
	Mode       string `json:"mode"`
	Status     string `json:"status"`
	Detail     string `json:"detail"`
}

func operationsCapabilities() []operationsCapabilityStatus {
	return []operationsCapabilityStatus{
		{
			Capability: "source_intake",
			Feature:    "Synthetic source intake",
			Mode:       "fixture",
			Status:     "available",
			Detail:     "Synthetic source envelopes are retained before normalization.",
		},
		{
			Capability: "luna_normalization",
			Feature:    "Luna normalization",
			Mode:       "fixture",
			Status:     "available",
			Detail:     "The fixture adapter records accepted, repaired, quarantined, or rejected lifecycle results.",
		},
		{
			Capability: "deterministic_projector",
			Feature:    "Deterministic projector",
			Mode:       "composed",
			Status:     "available",
			Detail:     "Only deterministic projection mutates the source-derived COP.",
		},
		{
			Capability: "startup_recovery",
			Feature:    "Startup and replay recovery",
			Mode:       "composed",
			Status:     "recovered",
			Detail:     "This response successfully recovered the COP from checkpoint and canonical history.",
		},
		{
			Capability: "terra_assessment",
			Feature:    "Terra structured assessment",
			Mode:       "unavailable",
			Status:     "unavailable",
			Detail:     "The structured service exists but is not live-composed in this demo.",
		},
		{
			Capability: "sol_advisory",
			Feature:    "Sol supervisor advisory",
			Mode:       "unavailable",
			Status:     "unavailable",
			Detail:     "The structured service exists but is not live-composed in this demo.",
		},
		{
			Capability: "human_audit",
			Feature:    "Human review audit",
			Mode:       "composed",
			Status:     "available",
			Detail:     "Review requests append immutable audit records and never execute an operational action.",
		},
		{
			Capability: "reconciliation",
			Feature:    "Durable reconciliation",
			Mode:       "unavailable",
			Status:     "unavailable",
			Detail:     "No durable reconciliation worker is composed in this single-instance demo.",
		},
		{
			Capability: "operational_action",
			Feature:    "Operational action",
			Mode:       "permanently_unavailable",
			Status:     "unavailable",
			Detail:     "Mosaic does not dispatch or mutate an external operational system in this demo.",
		},
	}
}

type unavailableOperationsReader struct{}

func (unavailableOperationsReader) ReadOperations(context.Context) (OperationsSnapshot, error) {
	return OperationsSnapshot{}, errors.New("operations reader is not composed")
}
