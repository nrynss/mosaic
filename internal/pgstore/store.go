// Package pgstore provides the append-only PostgreSQL implementation of
// Mosaic's durable repository contracts. It is a second, independent backend
// alongside the SQLite implementation in internal/store: application code
// depends only on the contracts in internal/contracts, so either backend can be
// wired in at composition without touching callers.
//
// Unlike the SQLite backend, pgstore uses a real connection pool. Postgres
// enforces foreign keys unconditionally and has no per-connection pragma, so
// the SQLite single-connection assumptions (SetMaxOpenConns(1), connection-local
// PRAGMA foreign_keys) are deliberately dropped here.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"mosaic.local/mosaic/internal/contracts"
	"mosaic.local/mosaic/internal/ontology/gen"
)

var (
	// ErrNotFound indicates that an immutable record has not been persisted.
	// It mirrors store.ErrNotFound so callers observe identical fail-open
	// behaviour regardless of backend.
	ErrNotFound = errors.New("pgstore record not found")

	// ErrInvalidRecord indicates that a record is missing a database-required
	// identity field. Full JSON Schema validation belongs to the ingestion and
	// adapter boundaries; these checks protect database identity and references.
	ErrInvalidRecord = errors.New("invalid pgstore record")
)

// Store is a PostgreSQL implementation of the Mosaic repository contracts. It
// holds a pgx connection pool; individual operations acquire connections as
// needed, and WithinTransaction pins one connection for the duration of a
// transaction.
type Store struct {
	pool *pgxpool.Pool
}

var (
	_ contracts.RawEventRepository        = (*Store)(nil)
	_ contracts.CanonicalEventRepository  = (*Store)(nil)
	_ contracts.ImmutableRecordRepository = (*Store)(nil)
	_ contracts.AdvisoryHistoryReader     = (*Store)(nil)
	_ contracts.CheckpointRepository      = (*Store)(nil)
	_ contracts.TransactionRunner         = (*Store)(nil)
)

// Open connects to the PostgreSQL database at dsn, verifies connectivity, and
// applies all ordered migrations. dsn is a libpq/pgx connection string or URL
// (for example "postgres://user:pass@host:5432/mosaic?sslmode=disable").
func Open(ctx context.Context, dsn string) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("%w: PostgreSQL DSN is required", ErrInvalidRecord)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	s := &Store{pool: pool}
	if err := s.configure(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := s.applyMigrations(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// NewFromPool adopts an already-configured pgx pool and applies migrations. It
// is useful when the caller owns pool lifecycle (for example a shared pool for
// the event spine and read model). The caller retains ownership of the pool;
// Close on the returned Store does not close a pool passed in here.
func NewFromPool(ctx context.Context, pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, fmt.Errorf("%w: pool is required", ErrInvalidRecord)
	}
	s := &Store{pool: pool}
	if err := s.applyMigrations(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Close releases the connection pool opened by Open. Pools passed to
// NewFromPool are owned by the caller and are not closed here.
func (s *Store) Close() error {
	if s.pool != nil {
		s.pool.Close()
	}
	return nil
}

// Pool exposes the underlying pgx pool for narrow operational checks and for
// later parcels (event spine, LISTEN/NOTIFY) that share the connection. Mosaic
// application code should depend on the repository contracts instead.
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

func (s *Store) configure(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping PostgreSQL database: %w", err)
	}
	return nil
}

func (s *Store) applyMigrations(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		migration_name TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		var present int
		err := s.pool.QueryRow(ctx,
			"SELECT 1 FROM schema_migrations WHERE migration_name = $1", entry.Name(),
		).Scan(&present)
		if err == nil {
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("check migration %s: %w", entry.Name(), err)
		}

		body, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", entry.Name(), err)
		}
		// The migration body contains multiple statements. pgx executes a
		// no-argument Exec via the simple query protocol, which permits
		// multiple statements in one round trip.
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.Exec(ctx,
			"INSERT INTO schema_migrations (migration_name, applied_at) VALUES ($1, $2)",
			entry.Name(), time.Now().UTC().Format(time.RFC3339Nano),
		); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

type transactionContextKey struct{}

type transactionContext struct {
	store *Store
	tx    pgx.Tx
}

// pgExecutor is the subset of the pgx API shared by *pgxpool.Pool and pgx.Tx,
// letting each repository method run either directly against the pool or inside
// an ambient transaction.
type pgExecutor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (s *Store) executor(ctx context.Context) (pgExecutor, error) {
	transaction, ok := ctx.Value(transactionContextKey{}).(transactionContext)
	if !ok {
		return s.pool, nil
	}
	if transaction.store != s {
		return nil, errors.New("store transaction belongs to a different Store")
	}
	return transaction.tx, nil
}

func (s *Store) inTransaction(ctx context.Context) bool {
	transaction, ok := ctx.Value(transactionContextKey{}).(transactionContext)
	return ok && transaction.store == s
}

// WithinTransaction runs fn in one serializable PostgreSQL transaction. Nested
// calls join the caller's transaction so the canonical-event, projection
// receipt, and checkpoint boundary from RFC-0001 §6.3 remains atomic, matching
// the SQLite backend's semantics.
func (s *Store) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("transaction callback is nil")
	}
	if s.inTransaction(ctx) {
		return fn(ctx)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	txCtx := context.WithValue(ctx, transactionContextKey{}, transactionContext{store: s, tx: tx})
	if err := fn(txCtx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// AppendRawEvent persists a source envelope. A duplicate source record (or,
// when no source record exists, duplicate caller-provided idempotency key)
// returns the existing raw event without adding history.
func (s *Store) AppendRawEvent(ctx context.Context, event gen.RawEvent) (contracts.RawEventAppendResult, error) {
	sourceID, sourceRecordID, idempotencyKey, err := rawIdentity(event)
	if err != nil {
		return contracts.RawEventAppendResult{}, err
	}
	exec, err := s.executor(ctx)
	if err != nil {
		return contracts.RawEventAppendResult{}, err
	}

	if existingID, found, err := findExistingRawEvent(ctx, exec, sourceID, sourceRecordID, idempotencyKey); err != nil {
		return contracts.RawEventAppendResult{}, err
	} else if found {
		resultID, err := findExistingLunaResult(ctx, exec, existingID)
		if err != nil {
			return contracts.RawEventAppendResult{}, err
		}
		return contracts.RawEventAppendResult{RawEventID: existingID, ExistingResultID: resultID}, nil
	}

	recordJSON, err := marshalRecord(event)
	if err != nil {
		return contracts.RawEventAppendResult{}, err
	}
	_, insertErr := exec.Exec(ctx, `INSERT INTO raw_events
		(raw_event_id, source_id, source_record_id, idempotency_key, record_json)
		VALUES ($1, $2, $3, $4, $5::jsonb)`,
		event.RawEventID, sourceID, nullableString(sourceRecordID), nullableString(idempotencyKey), recordJSON,
	)
	if insertErr == nil {
		return contracts.RawEventAppendResult{RawEventID: event.RawEventID, Created: true}, nil
	}

	// A concurrent delivery can win after the first lookup. Re-read the unique
	// idempotency key and report the persisted record when that is the cause.
	existingID, found, lookupErr := findExistingRawEvent(ctx, exec, sourceID, sourceRecordID, idempotencyKey)
	if lookupErr != nil {
		return contracts.RawEventAppendResult{}, lookupErr
	}
	if found {
		resultID, err := findExistingLunaResult(ctx, exec, existingID)
		if err != nil {
			return contracts.RawEventAppendResult{}, err
		}
		return contracts.RawEventAppendResult{RawEventID: existingID, ExistingResultID: resultID}, nil
	}
	return contracts.RawEventAppendResult{}, fmt.Errorf("append raw event: %w", insertErr)
}

// FindRawEvent retrieves the immutable source envelope by its event ID.
func (s *Store) FindRawEvent(ctx context.Context, rawEventID string) (gen.RawEvent, error) {
	exec, err := s.executor(ctx)
	if err != nil {
		return gen.RawEvent{}, err
	}
	var recordJSON string
	err = exec.QueryRow(ctx, "SELECT record_json FROM raw_events WHERE raw_event_id = $1", rawEventID).Scan(&recordJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.RawEvent{}, fmt.Errorf("%w: raw event %q", ErrNotFound, rawEventID)
	}
	if err != nil {
		return gen.RawEvent{}, fmt.Errorf("find raw event %q: %w", rawEventID, err)
	}
	var event gen.RawEvent
	if err := json.Unmarshal([]byte(recordJSON), &event); err != nil {
		return gen.RawEvent{}, fmt.Errorf("decode raw event %q: %w", rawEventID, err)
	}
	return event, nil
}

func rawIdentity(event gen.RawEvent) (sourceID, sourceRecordID, idempotencyKey string, err error) {
	if strings.TrimSpace(event.RawEventID) == "" {
		return "", "", "", fmt.Errorf("%w: raw_event_id is required", ErrInvalidRecord)
	}
	var ok bool
	sourceID, ok = event.Source["source_id"].(string)
	if !ok || strings.TrimSpace(sourceID) == "" {
		return "", "", "", fmt.Errorf("%w: source.source_id is required", ErrInvalidRecord)
	}
	sourceRecordID, _ = event.Source["source_record_id"].(string)
	idempotencyKey, _ = event.Source["idempotency_key"].(string)
	if strings.TrimSpace(sourceRecordID) == "" {
		sourceRecordID = ""
		if strings.TrimSpace(idempotencyKey) == "" {
			return "", "", "", fmt.Errorf("%w: source.idempotency_key is required when source_record_id is absent", ErrInvalidRecord)
		}
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		idempotencyKey = ""
	}
	return sourceID, sourceRecordID, idempotencyKey, nil
}

func findExistingRawEvent(ctx context.Context, exec pgExecutor, sourceID, sourceRecordID, idempotencyKey string) (string, bool, error) {
	query := "SELECT raw_event_id FROM raw_events WHERE source_id = $1 AND "
	var value string
	if sourceRecordID != "" {
		query += "source_record_id = $2"
		value = sourceRecordID
	} else {
		query += "source_record_id IS NULL AND idempotency_key = $2"
		value = idempotencyKey
	}
	var rawEventID string
	err := exec.QueryRow(ctx, query, sourceID, value).Scan(&rawEventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("find idempotent raw event: %w", err)
	}
	return rawEventID, true, nil
}

func findExistingLunaResult(ctx context.Context, exec pgExecutor, rawEventID string) (string, error) {
	var resultID string
	err := exec.QueryRow(ctx, `SELECT luna_result_id FROM luna_results
		WHERE raw_event_id = $1 ORDER BY ctid DESC LIMIT 1`, rawEventID).Scan(&resultID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("find existing Luna result: %w", err)
	}
	return resultID, nil
}

// AppendCanonicalEvent adds an event to the database-owned canonical sequence.
// The incoming CanonicalSeq is ignored: the persisted append sequence is the
// only projection/replay order.
func (s *Store) AppendCanonicalEvent(ctx context.Context, event gen.CanonicalEvent) (gen.CanonicalEvent, error) {
	if s.inTransaction(ctx) {
		return s.appendCanonicalEvent(ctx, event)
	}
	var appended gen.CanonicalEvent
	err := s.WithinTransaction(ctx, func(txCtx context.Context) error {
		var err error
		appended, err = s.appendCanonicalEvent(txCtx, event)
		return err
	})
	if err != nil {
		return gen.CanonicalEvent{}, err
	}
	return appended, nil
}

func (s *Store) appendCanonicalEvent(ctx context.Context, event gen.CanonicalEvent) (gen.CanonicalEvent, error) {
	if strings.TrimSpace(event.CanonicalEventID) == "" || strings.TrimSpace(event.RawEventID) == "" {
		return gen.CanonicalEvent{}, fmt.Errorf("%w: canonical_event_id and raw_event_id are required", ErrInvalidRecord)
	}
	exec, err := s.executor(ctx)
	if err != nil {
		return gen.CanonicalEvent{}, err
	}
	sequence, err := nextCanonicalSequence(ctx, exec)
	if err != nil {
		return gen.CanonicalEvent{}, err
	}
	stored := event
	stored.CanonicalSeq = sequence
	recordJSON, err := marshalRecord(stored)
	if err != nil {
		return gen.CanonicalEvent{}, err
	}
	if _, err := exec.Exec(ctx, `INSERT INTO canonical_events
		(canonical_seq, canonical_event_id, raw_event_id, supersedes_event_id, record_json)
		VALUES ($1, $2, $3, $4, $5::jsonb)`,
		sequence, stored.CanonicalEventID, stored.RawEventID, nullableString(stored.SupersedesEventID), recordJSON,
	); err != nil {
		return gen.CanonicalEvent{}, fmt.Errorf("append canonical event: %w", err)
	}

	seenIncidentIDs := make(map[string]struct{}, len(stored.IncidentRefs))
	for _, reference := range stored.IncidentRefs {
		incidentID, ok := reference.(string)
		if !ok || strings.TrimSpace(incidentID) == "" {
			return gen.CanonicalEvent{}, fmt.Errorf("%w: incident_refs must contain non-empty strings", ErrInvalidRecord)
		}
		if _, seen := seenIncidentIDs[incidentID]; seen {
			continue
		}
		seenIncidentIDs[incidentID] = struct{}{}
		if _, err := exec.Exec(ctx, `INSERT INTO canonical_event_incidents
			(canonical_event_id, incident_id) VALUES ($1, $2)`, stored.CanonicalEventID, incidentID); err != nil {
			return gen.CanonicalEvent{}, fmt.Errorf("index canonical event incident: %w", err)
		}
	}
	return stored, nil
}

// nextCanonicalSequence derives the next append position from the persisted
// maximum. AppendCanonicalEvent always runs inside a serializable transaction,
// so concurrent appends serialize just as the SQLite single-connection backend
// serializes writers; the database owns the order, not the caller.
func nextCanonicalSequence(ctx context.Context, exec pgExecutor) (int64, error) {
	var next int64
	err := exec.QueryRow(ctx, "SELECT COALESCE(MAX(canonical_seq), 0) + 1 FROM canonical_events").Scan(&next)
	if err != nil {
		return 0, fmt.Errorf("read canonical sequence: %w", err)
	}
	return next, nil
}

// ListCanonicalEventsAfter returns the durable canonical log in ascending
// database sequence, never by domain occurrence time.
func (s *Store) ListCanonicalEventsAfter(ctx context.Context, canonicalSeq int64) ([]gen.CanonicalEvent, error) {
	exec, err := s.executor(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := exec.Query(ctx, `SELECT canonical_seq, record_json FROM canonical_events
		WHERE canonical_seq > $1 ORDER BY canonical_seq ASC`, canonicalSeq)
	if err != nil {
		return nil, fmt.Errorf("list canonical events after %d: %w", canonicalSeq, err)
	}
	defer rows.Close()

	var events []gen.CanonicalEvent
	for rows.Next() {
		var sequence int64
		var recordJSON string
		if err := rows.Scan(&sequence, &recordJSON); err != nil {
			return nil, fmt.Errorf("scan canonical event: %w", err)
		}
		var event gen.CanonicalEvent
		if err := json.Unmarshal([]byte(recordJSON), &event); err != nil {
			return nil, fmt.Errorf("decode canonical event at sequence %d: %w", sequence, err)
		}
		event.CanonicalSeq = sequence
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate canonical events: %w", err)
	}
	return events, nil
}

// ListEffectiveCanonicalEventsForIncident resolves correction branches from the
// durable log. A leaf is effective only if every correction in its ancestry is
// the highest-sequence direct correction for its predecessor.
func (s *Store) ListEffectiveCanonicalEventsForIncident(ctx context.Context, incidentID string) ([]gen.CanonicalEvent, error) {
	if strings.TrimSpace(incidentID) == "" {
		return nil, fmt.Errorf("%w: incident ID is required", ErrInvalidRecord)
	}
	events, err := s.ListCanonicalEventsAfter(ctx, 0)
	if err != nil {
		return nil, err
	}
	return effectiveEventsForIncident(events, incidentID), nil
}

func effectiveEventsForIncident(events []gen.CanonicalEvent, incidentID string) []gen.CanonicalEvent {
	byID := make(map[string]gen.CanonicalEvent, len(events))
	children := make(map[string][]gen.CanonicalEvent)
	for _, event := range events {
		byID[event.CanonicalEventID] = event
		if event.SupersedesEventID != "" {
			children[event.SupersedesEventID] = append(children[event.SupersedesEventID], event)
		}
	}
	preferredChild := make(map[string]string, len(children))
	for parentID, directCorrections := range children {
		sort.Slice(directCorrections, func(i, j int) bool {
			return directCorrections[i].CanonicalSeq < directCorrections[j].CanonicalSeq
		})
		preferredChild[parentID] = directCorrections[len(directCorrections)-1].CanonicalEventID
	}

	var effective []gen.CanonicalEvent
	for _, event := range events {
		if len(children[event.CanonicalEventID]) != 0 || !hasPreferredAncestry(event, byID, preferredChild) {
			continue
		}
		if belongsToIncident(event, incidentID, byID) {
			effective = append(effective, event)
		}
	}
	sort.Slice(effective, func(i, j int) bool { return effective[i].CanonicalSeq < effective[j].CanonicalSeq })
	return effective
}

func hasPreferredAncestry(event gen.CanonicalEvent, byID map[string]gen.CanonicalEvent, preferredChild map[string]string) bool {
	current := event
	visited := make(map[string]struct{})
	for current.SupersedesEventID != "" {
		if _, seen := visited[current.CanonicalEventID]; seen {
			return false
		}
		visited[current.CanonicalEventID] = struct{}{}
		if preferredChild[current.SupersedesEventID] != current.CanonicalEventID {
			return false
		}
		parent, exists := byID[current.SupersedesEventID]
		if !exists {
			return false
		}
		current = parent
	}
	return true
}

func belongsToIncident(event gen.CanonicalEvent, incidentID string, byID map[string]gen.CanonicalEvent) bool {
	current := event
	visited := make(map[string]struct{})
	for {
		if _, seen := visited[current.CanonicalEventID]; seen {
			return false
		}
		visited[current.CanonicalEventID] = struct{}{}
		for _, reference := range current.IncidentRefs {
			if referenceID, ok := reference.(string); ok && referenceID == incidentID {
				return true
			}
		}
		if current.SupersedesEventID == "" {
			return false
		}
		parent, exists := byID[current.SupersedesEventID]
		if !exists {
			return false
		}
		current = parent
	}
}

// MarkCanonicalEventProjected appends an immutable projection receipt. A retry
// for the same state revision is idempotent; a conflicting revision is refused.
func (s *Store) MarkCanonicalEventProjected(ctx context.Context, canonicalEventID string, stateRevision int64) error {
	if strings.TrimSpace(canonicalEventID) == "" || stateRevision < 1 {
		return fmt.Errorf("%w: canonical event ID and state revision are required", ErrInvalidRecord)
	}
	exec, err := s.executor(ctx)
	if err != nil {
		return err
	}
	var existingRevision int64
	err = exec.QueryRow(ctx, `SELECT state_revision FROM canonical_projection_receipts
		WHERE canonical_event_id = $1`, canonicalEventID).Scan(&existingRevision)
	if err == nil {
		if existingRevision == stateRevision {
			return nil
		}
		return fmt.Errorf("canonical event %q already projected at state revision %d", canonicalEventID, existingRevision)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read projection receipt: %w", err)
	}
	if _, err := exec.Exec(ctx, `INSERT INTO canonical_projection_receipts
		(canonical_event_id, state_revision, recorded_at) VALUES ($1, $2, $3)`,
		canonicalEventID, stateRevision, time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("append projection receipt: %w", err)
	}
	return nil
}

// AppendLunaResult persists a structured normalizer lifecycle record.
func (s *Store) AppendLunaResult(ctx context.Context, result gen.LunaResult) error {
	if strings.TrimSpace(result.LunaResultID) == "" || strings.TrimSpace(result.RawEventID) == "" {
		return fmt.Errorf("%w: luna_result_id and raw_event_id are required", ErrInvalidRecord)
	}
	recordJSON, err := marshalRecord(result)
	if err != nil {
		return err
	}
	exec, err := s.executor(ctx)
	if err != nil {
		return err
	}
	_, err = exec.Exec(ctx, `INSERT INTO luna_results
		(luna_result_id, raw_event_id, canonical_event_id, record_json) VALUES ($1, $2, $3, $4::jsonb)`,
		result.LunaResultID, result.RawEventID, nullableString(result.CanonicalEventID), recordJSON,
	)
	if err != nil {
		return fmt.Errorf("append Luna result: %w", err)
	}
	return nil
}

// AppendInsight persists an immutable Terra assessment.
func (s *Store) AppendInsight(ctx context.Context, insight gen.Insight) error {
	if strings.TrimSpace(insight.InsightID) == "" || insight.StateRevision < 1 {
		return fmt.Errorf("%w: insight_id and state_revision are required", ErrInvalidRecord)
	}
	return s.appendJSONRecord(ctx, "insights", "insight_id", insight.InsightID, insight.StateRevision, insight)
}

// AppendRecommendation persists an immutable supervisor-review option.
func (s *Store) AppendRecommendation(ctx context.Context, recommendation gen.Recommendation) error {
	if strings.TrimSpace(recommendation.RecommendationID) == "" || recommendation.StateRevision < 1 {
		return fmt.Errorf("%w: recommendation_id and state_revision are required", ErrInvalidRecord)
	}
	return s.appendJSONRecord(ctx, "recommendations", "recommendation_id", recommendation.RecommendationID, recommendation.StateRevision, recommendation)
}

// AppendModelRun persists model invocation provenance, including failures and
// refusals that intentionally produce no operational state mutation.
func (s *Store) AppendModelRun(ctx context.Context, run gen.ModelRun) error {
	if strings.TrimSpace(run.ModelRunID) == "" {
		return fmt.Errorf("%w: model_run_id is required", ErrInvalidRecord)
	}
	recordJSON, err := marshalRecord(run)
	if err != nil {
		return err
	}
	exec, err := s.executor(ctx)
	if err != nil {
		return err
	}
	var stateRevision any
	if run.StateRevision > 0 {
		stateRevision = run.StateRevision
	}
	if _, err := exec.Exec(ctx, `INSERT INTO model_runs
		(model_run_id, state_revision, record_json) VALUES ($1, $2, $3::jsonb)`, run.ModelRunID, stateRevision, recordJSON); err != nil {
		return fmt.Errorf("append model run: %w", err)
	}
	return nil
}

// AppendAuditRecord persists an immutable human or system audit entry.
func (s *Store) AppendAuditRecord(ctx context.Context, audit gen.AuditRecord) error {
	if strings.TrimSpace(audit.AuditRecordID) == "" {
		return fmt.Errorf("%w: audit_record_id is required", ErrInvalidRecord)
	}
	recordJSON, err := marshalRecord(audit)
	if err != nil {
		return err
	}
	exec, err := s.executor(ctx)
	if err != nil {
		return err
	}
	if _, err := exec.Exec(ctx, "INSERT INTO audit_records (audit_record_id, record_json) VALUES ($1, $2::jsonb)", audit.AuditRecordID, recordJSON); err != nil {
		return fmt.Errorf("append audit record: %w", err)
	}
	return nil
}

// ReadAdvisoryHistory returns the persisted advisory-domain records in stable
// chronological order. It deliberately reads only immutable advisory records;
// raw events, canonical events, and source payloads remain outside this seam.
func (s *Store) ReadAdvisoryHistory(ctx context.Context) (contracts.AdvisoryHistory, error) {
	exec, err := s.executor(ctx)
	if err != nil {
		return contracts.AdvisoryHistory{}, err
	}

	insights, err := readAdvisoryRecords(ctx, exec, `SELECT insight_id, record_json FROM insights
		ORDER BY insight_id ASC`, "insight", func(insight gen.Insight) string { return insight.CreatedAt })
	if err != nil {
		return contracts.AdvisoryHistory{}, err
	}
	recommendations, err := readAdvisoryRecords(ctx, exec, `SELECT recommendation_id, record_json FROM recommendations
		ORDER BY recommendation_id ASC`, "recommendation", func(recommendation gen.Recommendation) string { return recommendation.CreatedAt })
	if err != nil {
		return contracts.AdvisoryHistory{}, err
	}
	modelRuns, err := readAdvisoryRecords(ctx, exec, `SELECT model_run_id, record_json FROM model_runs
		WHERE record_json->>'agent' IN ('terra', 'sol')
		ORDER BY model_run_id ASC`, "model run", func(run gen.ModelRun) string { return run.CompletedAt })
	if err != nil {
		return contracts.AdvisoryHistory{}, err
	}
	auditRecords, err := readAdvisoryRecords(ctx, exec, `SELECT audit_record_id, record_json FROM audit_records
		ORDER BY audit_record_id ASC`, "audit record", func(record gen.AuditRecord) string { return record.CreatedAt })
	if err != nil {
		return contracts.AdvisoryHistory{}, err
	}

	return contracts.AdvisoryHistory{
		Insights:        insights,
		Recommendations: recommendations,
		ModelRuns:       modelRuns,
		AuditRecords:    auditRecords,
	}, nil
}

type advisoryRecord[T any] struct {
	record     T
	occurredAt time.Time
	recordID   string
}

func readAdvisoryRecords[T any](ctx context.Context, exec pgExecutor, query, recordType string, timestamp func(T) string) ([]T, error) {
	rows, err := exec.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list advisory %ss: %w", recordType, err)
	}
	defer rows.Close()

	records := make([]advisoryRecord[T], 0)
	for rows.Next() {
		var recordID, recordJSON string
		if err := rows.Scan(&recordID, &recordJSON); err != nil {
			return nil, fmt.Errorf("scan advisory %s: %w", recordType, err)
		}
		var record T
		if err := json.Unmarshal([]byte(recordJSON), &record); err != nil {
			return nil, fmt.Errorf("decode advisory %s: %w", recordType, err)
		}
		occurredAt, err := time.Parse(time.RFC3339Nano, timestamp(record))
		if err != nil {
			return nil, fmt.Errorf("parse advisory %s timestamp for %q: %w", recordType, recordID, err)
		}
		records = append(records, advisoryRecord[T]{
			record:     record,
			occurredAt: occurredAt.UTC(),
			recordID:   recordID,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate advisory %ss: %w", recordType, err)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].occurredAt.Equal(records[j].occurredAt) {
			return records[i].recordID < records[j].recordID
		}
		return records[i].occurredAt.Before(records[j].occurredAt)
	})

	ordered := make([]T, 0, len(records))
	for _, record := range records {
		ordered = append(ordered, record.record)
	}
	return ordered, nil
}

func (s *Store) appendJSONRecord(ctx context.Context, table, idColumn, id string, stateRevision int64, record any) error {
	recordJSON, err := marshalRecord(record)
	if err != nil {
		return err
	}
	exec, err := s.executor(ctx)
	if err != nil {
		return err
	}
	statement := fmt.Sprintf("INSERT INTO %s (%s, state_revision, record_json) VALUES ($1, $2, $3::jsonb)", table, idColumn)
	if _, err := exec.Exec(ctx, statement, id, stateRevision, recordJSON); err != nil {
		return fmt.Errorf("append %s: %w", table, err)
	}
	return nil
}

// AppendCheckpoint persists a serializable COP snapshot for deterministic
// recovery. A checkpoint never replaces an earlier snapshot.
func (s *Store) AppendCheckpoint(ctx context.Context, checkpoint gen.Checkpoint) error {
	if strings.TrimSpace(checkpoint.CheckpointID) == "" || checkpoint.StateRevision < 1 || checkpoint.ThroughCanonicalSeq < 0 {
		return fmt.Errorf("%w: checkpoint ID, state revision, and sequence are required", ErrInvalidRecord)
	}
	recordJSON, err := marshalRecord(checkpoint)
	if err != nil {
		return err
	}
	exec, err := s.executor(ctx)
	if err != nil {
		return err
	}
	if _, err := exec.Exec(ctx, `INSERT INTO checkpoints
		(checkpoint_id, state_revision, through_canonical_seq, record_json) VALUES ($1, $2, $3, $4::jsonb)`,
		checkpoint.CheckpointID, checkpoint.StateRevision, checkpoint.ThroughCanonicalSeq, recordJSON,
	); err != nil {
		return fmt.Errorf("append checkpoint: %w", err)
	}
	return nil
}

// LatestCheckpoint retrieves the highest state-revision checkpoint, or
// ErrNotFound before the first successful projection.
func (s *Store) LatestCheckpoint(ctx context.Context) (gen.Checkpoint, error) {
	exec, err := s.executor(ctx)
	if err != nil {
		return gen.Checkpoint{}, err
	}
	var recordJSON string
	err = exec.QueryRow(ctx, `SELECT record_json FROM checkpoints
		ORDER BY state_revision DESC LIMIT 1`).Scan(&recordJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.Checkpoint{}, fmt.Errorf("%w: checkpoint", ErrNotFound)
	}
	if err != nil {
		return gen.Checkpoint{}, fmt.Errorf("read latest checkpoint: %w", err)
	}
	var checkpoint gen.Checkpoint
	if err := json.Unmarshal([]byte(recordJSON), &checkpoint); err != nil {
		return gen.Checkpoint{}, fmt.Errorf("decode latest checkpoint: %w", err)
	}
	return checkpoint, nil
}

func marshalRecord(record any) (string, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("encode immutable record: %w", err)
	}
	return string(encoded), nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
