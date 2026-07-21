package pgstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
)

// testDSNEnv gates the PostgreSQL integration tests. When unset, every test
// skips cleanly so `go test ./...` passes without a database. To run them,
// point it at a reachable Postgres, for example:
//
//	MOSAIC_TEST_PG_DSN="postgres://mosaic:mosaic@localhost:5432/mosaic?sslmode=disable" \
//	    go test ./internal/pgstore/...
//
// Each test provisions its own throwaway schema, applies the migrations into
// it, and drops it on cleanup, so runs are isolated and repeatable against a
// shared database.
const testDSNEnv = "MOSAIC_TEST_PG_DSN"

var schemaCounter atomic.Int64

// newTestStore provisions an isolated schema on the target database, opens a
// pool scoped to it via search_path, applies migrations, and registers cleanup.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(testDSNEnv))
	if dsn == "" {
		t.Skipf("%s not set; skipping PostgreSQL integration test", testDSNEnv)
	}
	ctx := context.Background()

	schema := fmt.Sprintf("mosaic_test_%d_%d", time.Now().UnixNano(), schemaCounter.Add(1))

	bootstrap, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect bootstrap: %v", err)
	}
	if _, err := bootstrap.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", pgIdent(schema))); err != nil {
		_ = bootstrap.Close(ctx)
		t.Fatalf("create test schema: %v", err)
	}
	_ = bootstrap.Close(ctx)

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	// Scope every pooled connection to the throwaway schema so migrations and
	// writes land there and never touch other tests or real data.
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}

	s, err := NewFromPool(ctx, pool)
	if err != nil {
		pool.Close()
		dropSchema(t, dsn, schema)
		t.Fatalf("apply migrations: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		dropSchema(t, dsn, schema)
	})
	return s
}

func dropSchema(t *testing.T, dsn, schema string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Errorf("connect to drop schema: %v", err)
		return
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, fmt.Sprintf("DROP SCHEMA %s CASCADE", pgIdent(schema))); err != nil {
		t.Errorf("drop test schema %s: %v", schema, err)
	}
}

// pgIdent double-quotes an identifier we generate. The schema name is composed
// only of ASCII letters, digits and underscores, but quoting keeps the DDL
// robust and makes the intent explicit.
func pgIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func TestOpenAppliesMigrationsAndRejectsHistoryMutation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	tables := []string{
		"raw_events", "canonical_events", "luna_results", "insights", "recommendations",
		"model_runs", "audit_records", "checkpoints", "canonical_projection_receipts",
		// Event spine transport (migration 0002; B2/B3 implement against these).
		"event_log", "event_consumer_checkpoints",
	}
	for _, table := range tables {
		var regclass string
		err := s.Pool().QueryRow(ctx, "SELECT to_regclass($1)::text", table).Scan(&regclass)
		if err != nil {
			t.Fatalf("look up %s: %v", table, err)
		}
		if regclass == "" {
			t.Fatalf("migration did not create %s", table)
		}
	}

	if _, err := s.AppendRawEvent(ctx, rawEvent("raw-immutable", "dispatch", "source-immutable", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, "UPDATE raw_events SET source_id = 'changed' WHERE raw_event_id = 'raw-immutable'"); err == nil {
		t.Fatal("append-only trigger allowed UPDATE")
	}
	if _, err := s.Pool().Exec(ctx, "DELETE FROM raw_events WHERE raw_event_id = 'raw-immutable'"); err == nil {
		t.Fatal("append-only trigger allowed DELETE")
	}
}

func TestRawEventIdempotencyReturnsExistingLifecycleResult(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	first := rawEvent("raw-1", "dispatch", "record-1", "")

	appended, err := s.AppendRawEvent(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if !appended.Created || appended.RawEventID != first.RawEventID {
		t.Fatalf("first append = %#v, want a created raw-1", appended)
	}
	if err := s.AppendLunaResult(ctx, gen.LunaResult{LunaResultID: "luna-1", RawEventID: first.RawEventID}); err != nil {
		t.Fatal(err)
	}

	duplicate, err := s.AppendRawEvent(ctx, rawEvent("raw-duplicate", "dispatch", "record-1", ""))
	if err != nil {
		t.Fatal(err)
	}
	want := contracts.RawEventAppendResult{RawEventID: "raw-1", ExistingResultID: "luna-1"}
	if duplicate != want {
		t.Fatalf("duplicate append = %#v, want %#v", duplicate, want)
	}
	if _, err := s.FindRawEvent(ctx, "raw-duplicate"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("duplicate created another source record: %v", err)
	}
}

func TestCanonicalSequenceIsDatabaseOrdered(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	appendRaw(t, s, rawEvent("raw-1", "dispatch", "record-1", ""))
	appendRaw(t, s, rawEvent("raw-2", "dispatch", "record-2", ""))

	first, err := s.AppendCanonicalEvent(ctx, canonicalEvent("event-1", "raw-1", "incident-1", ""))
	if err != nil {
		t.Fatal(err)
	}
	secondInput := canonicalEvent("event-2", "raw-2", "incident-1", "")
	secondInput.CanonicalSeq = 9000
	second, err := s.AppendCanonicalEvent(ctx, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if first.CanonicalSeq != 1 || second.CanonicalSeq != 2 {
		t.Fatalf("canonical sequences = %d, %d; want 1, 2", first.CanonicalSeq, second.CanonicalSeq)
	}

	events, err := s.ListCanonicalEventsAfter(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].CanonicalEventID != "event-1" || events[1].CanonicalEventID != "event-2" {
		t.Fatalf("canonical append order = %#v", events)
	}
	if events[1].CanonicalSeq != 2 {
		t.Fatalf("stored canonical sequence = %d, want 2", events[1].CanonicalSeq)
	}
}

func TestEffectiveEventsChooseHighestDirectCorrection(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	for i := 1; i <= 4; i++ {
		appendRaw(t, s, rawEvent(fmt.Sprintf("raw-%d", i), "dispatch", fmt.Sprintf("record-%d", i), ""))
	}
	appendCanonical(t, s, canonicalEvent("original", "raw-1", "incident-1", ""))
	appendCanonical(t, s, canonicalEvent("unrelated", "raw-2", "incident-1", ""))
	appendCanonical(t, s, canonicalEvent("lower-correction", "raw-3", "incident-1", "original"))
	appendCanonical(t, s, canonicalEvent("higher-correction", "raw-4", "incident-1", "original"))

	effective, err := s.ListEffectiveCanonicalEventsForIncident(ctx, "incident-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(effective) != 2 {
		t.Fatalf("effective events = %#v, want unrelated plus highest correction", effective)
	}
	if effective[0].CanonicalEventID != "unrelated" || effective[1].CanonicalEventID != "higher-correction" {
		t.Fatalf("effective event IDs = %q, %q; want unrelated, higher-correction", effective[0].CanonicalEventID, effective[1].CanonicalEventID)
	}
}

func TestTransactionRollbackLeavesNoDurableRecord(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	wantErr := errors.New("abort test transaction")
	err := s.WithinTransaction(ctx, func(txCtx context.Context) error {
		if _, err := s.AppendRawEvent(txCtx, rawEvent("raw-rollback", "dispatch", "record-rollback", "")); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("transaction error = %v, want %v", err, wantErr)
	}
	if _, err := s.FindRawEvent(ctx, "raw-rollback"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled-back event was persisted: %v", err)
	}
}

func TestLatestCheckpointUsesHighestStateRevision(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.AppendCheckpoint(ctx, checkpoint("checkpoint-1", 1, 3)); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendCheckpoint(ctx, checkpoint("checkpoint-2", 2, 8)); err != nil {
		t.Fatal(err)
	}

	latest, err := s.LatestCheckpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if latest.CheckpointID != "checkpoint-2" || latest.StateRevision != 2 || latest.ThroughCanonicalSeq != 8 {
		t.Fatalf("latest checkpoint = %#v", latest)
	}
}

func TestLatestCheckpointBeforeFirstProjection(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.LatestCheckpoint(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LatestCheckpoint before any projection = %v, want ErrNotFound", err)
	}
}

func TestReadAdvisoryHistoryEmpty(t *testing.T) {
	s := newTestStore(t)

	history, err := s.ReadAdvisoryHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if history.Insights == nil || history.Recommendations == nil || history.ModelRuns == nil || history.AuditRecords == nil {
		t.Fatalf("empty history contains unusable nil collections: %#v", history)
	}
	if len(history.Insights) != 0 || len(history.Recommendations) != 0 || len(history.ModelRuns) != 0 || len(history.AuditRecords) != 0 {
		t.Fatalf("empty history = %#v", history)
	}
}

func TestReadAdvisoryHistoryFiltersAndOrdersRecords(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	for _, insight := range []gen.Insight{
		{InsightID: "insight-later", StateRevision: 7, CreatedAt: "2026-07-19T10:00:00Z"},
		{InsightID: "insight-b", StateRevision: 7, CreatedAt: "2026-07-19T09:00:00Z"},
		{InsightID: "insight-offset", StateRevision: 7, CreatedAt: "2026-07-19T08:30:00-01:00"},
		{InsightID: "insight-a", StateRevision: 7, CreatedAt: "2026-07-19T09:00:00Z"},
	} {
		if err := s.AppendInsight(ctx, insight); err != nil {
			t.Fatal(err)
		}
	}
	for _, recommendation := range []gen.Recommendation{
		{RecommendationID: "recommendation-later", StateRevision: 7, CreatedAt: "2026-07-19T10:00:00Z"},
		{RecommendationID: "recommendation-b", StateRevision: 7, CreatedAt: "2026-07-19T09:00:00Z"},
		{RecommendationID: "recommendation-a", StateRevision: 7, CreatedAt: "2026-07-19T09:00:00Z"},
	} {
		if err := s.AppendRecommendation(ctx, recommendation); err != nil {
			t.Fatal(err)
		}
	}
	// This malformed Luna timestamp must stay outside the advisory query.
	for _, run := range []gen.ModelRun{
		{ModelRunID: "model-run-luna", Agent: "luna", CompletedAt: "not-a-timestamp"},
		{ModelRunID: "model-run-later", Agent: "sol", CompletedAt: "2026-07-19T10:00:00Z"},
		{ModelRunID: "model-run-b", Agent: "terra", CompletedAt: "2026-07-19T09:00:00Z"},
		{ModelRunID: "model-run-a", Agent: "sol", CompletedAt: "2026-07-19T09:00:00Z"},
	} {
		if err := s.AppendModelRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	for _, audit := range []gen.AuditRecord{
		{AuditRecordID: "audit-later", CreatedAt: "2026-07-19T10:00:00Z"},
		{AuditRecordID: "audit-b", CreatedAt: "2026-07-19T09:00:00Z"},
		{AuditRecordID: "audit-a", CreatedAt: "2026-07-19T09:00:00Z"},
	} {
		if err := s.AppendAuditRecord(ctx, audit); err != nil {
			t.Fatal(err)
		}
	}

	history, err := s.ReadAdvisoryHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Insights) != 4 || history.Insights[0].InsightID != "insight-a" || history.Insights[1].InsightID != "insight-b" || history.Insights[2].InsightID != "insight-offset" || history.Insights[3].InsightID != "insight-later" {
		t.Errorf("insight order = %#v", history.Insights)
	}
	if len(history.Recommendations) != 3 || history.Recommendations[0].RecommendationID != "recommendation-a" || history.Recommendations[1].RecommendationID != "recommendation-b" || history.Recommendations[2].RecommendationID != "recommendation-later" {
		t.Errorf("recommendation order = %#v", history.Recommendations)
	}
	if len(history.ModelRuns) != 3 || history.ModelRuns[0].ModelRunID != "model-run-a" || history.ModelRuns[1].ModelRunID != "model-run-b" || history.ModelRuns[2].ModelRunID != "model-run-later" {
		t.Errorf("advisory model-run order = %#v", history.ModelRuns)
	}
	if len(history.AuditRecords) != 3 || history.AuditRecords[0].AuditRecordID != "audit-a" || history.AuditRecords[1].AuditRecordID != "audit-b" || history.AuditRecords[2].AuditRecordID != "audit-later" {
		t.Errorf("audit record order = %#v", history.AuditRecords)
	}
}

func TestReadAdvisoryHistoryFailsClosedForMalformedStoredJSON(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if _, err := s.Pool().Exec(ctx, `INSERT INTO insights
		(insight_id, state_revision, record_json) VALUES ($1, $2, $3::jsonb)`, "malformed-insight", 1, `[]`); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ReadAdvisoryHistory(ctx); err == nil {
		t.Fatal("ReadAdvisoryHistory accepted malformed stored JSON")
	}
}

func TestReadAdvisoryHistoryFailsClosedForInvalidTimestamp(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.AppendInsight(ctx, gen.Insight{
		InsightID: "invalid-timestamp-insight", StateRevision: 1, CreatedAt: "not-a-timestamp",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ReadAdvisoryHistory(ctx); err == nil {
		t.Fatal("ReadAdvisoryHistory accepted an invalid timestamp")
	}
}

func rawEvent(id, sourceID, sourceRecordID, idempotencyKey string) gen.RawEvent {
	source := map[string]any{"source_id": sourceID}
	if sourceRecordID != "" {
		source["source_record_id"] = sourceRecordID
	}
	if idempotencyKey != "" {
		source["idempotency_key"] = idempotencyKey
	}
	return gen.RawEvent{
		RawEventID: id,
		Source:     source,
	}
}

func canonicalEvent(id, rawEventID, incidentID, supersedesEventID string) gen.CanonicalEvent {
	return gen.CanonicalEvent{
		CanonicalEventID:  id,
		RawEventID:        rawEventID,
		IncidentRefs:      []any{incidentID},
		SupersedesEventID: supersedesEventID,
	}
}

func checkpoint(id string, stateRevision, throughCanonicalSeq int64) gen.Checkpoint {
	return gen.Checkpoint{
		CheckpointID:        id,
		StateRevision:       stateRevision,
		ThroughCanonicalSeq: throughCanonicalSeq,
		COP:                 []byte(`{"state_revision":1}`),
	}
}

func appendRaw(t *testing.T, s *Store, event gen.RawEvent) {
	t.Helper()
	if _, err := s.AppendRawEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
}

func appendCanonical(t *testing.T, s *Store, event gen.CanonicalEvent) gen.CanonicalEvent {
	t.Helper()
	appended, err := s.AppendCanonicalEvent(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	return appended
}
