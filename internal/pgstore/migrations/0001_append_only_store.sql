-- PostgreSQL port of the append-only Mosaic store schema.
--
-- This mirrors migrations/0001_append_only_store.sql (SQLite) statement for
-- statement, adapted to Postgres-native constructs:
--   * record_json is JSONB (native JSON validation replaces the SQLite
--     `CHECK (json_valid(...))` guards);
--   * append-only enforcement uses a shared trigger function that raises an
--     exception on UPDATE/DELETE, replacing the SQLite `RAISE(ABORT, ...)`
--     triggers;
--   * canonical_seq is an application-assigned BIGINT (the pgstore computes the
--     next value inside the append transaction, matching how the SQLite backend
--     derives it from sqlite_sequence);
--   * partial unique indexes and foreign keys port unchanged — Postgres always
--     enforces foreign keys, so there is no per-connection pragma.

CREATE TABLE IF NOT EXISTS schema_migrations (
    migration_name TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL
);

CREATE TABLE raw_events (
    raw_event_id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL,
    source_record_id TEXT,
    idempotency_key TEXT,
    record_json JSONB NOT NULL
);

CREATE UNIQUE INDEX raw_events_source_record_id_unique
    ON raw_events(source_id, source_record_id)
    WHERE source_record_id IS NOT NULL;

CREATE UNIQUE INDEX raw_events_idempotency_key_unique
    ON raw_events(source_id, idempotency_key)
    WHERE source_record_id IS NULL AND idempotency_key IS NOT NULL;

CREATE TABLE canonical_events (
    canonical_seq BIGINT PRIMARY KEY,
    canonical_event_id TEXT NOT NULL UNIQUE,
    raw_event_id TEXT NOT NULL REFERENCES raw_events(raw_event_id),
    supersedes_event_id TEXT REFERENCES canonical_events(canonical_event_id),
    record_json JSONB NOT NULL,
    CHECK (supersedes_event_id IS NULL OR supersedes_event_id <> canonical_event_id)
);

CREATE INDEX canonical_events_raw_event_id_idx ON canonical_events(raw_event_id);
CREATE INDEX canonical_events_supersedes_event_id_idx ON canonical_events(supersedes_event_id);

CREATE TABLE canonical_event_incidents (
    canonical_event_id TEXT NOT NULL REFERENCES canonical_events(canonical_event_id),
    incident_id TEXT NOT NULL,
    PRIMARY KEY (canonical_event_id, incident_id)
);

CREATE INDEX canonical_event_incidents_incident_id_idx
    ON canonical_event_incidents(incident_id);

CREATE TABLE canonical_projection_receipts (
    canonical_event_id TEXT PRIMARY KEY REFERENCES canonical_events(canonical_event_id),
    state_revision BIGINT NOT NULL CHECK (state_revision >= 1),
    recorded_at TEXT NOT NULL
);

CREATE TABLE luna_results (
    luna_result_id TEXT PRIMARY KEY,
    raw_event_id TEXT NOT NULL REFERENCES raw_events(raw_event_id),
    canonical_event_id TEXT REFERENCES canonical_events(canonical_event_id),
    record_json JSONB NOT NULL
);

CREATE INDEX luna_results_raw_event_id_idx ON luna_results(raw_event_id);

CREATE TABLE insights (
    insight_id TEXT PRIMARY KEY,
    state_revision BIGINT NOT NULL CHECK (state_revision >= 1),
    record_json JSONB NOT NULL
);

CREATE INDEX insights_state_revision_idx ON insights(state_revision);

CREATE TABLE recommendations (
    recommendation_id TEXT PRIMARY KEY,
    state_revision BIGINT NOT NULL CHECK (state_revision >= 1),
    record_json JSONB NOT NULL
);

CREATE INDEX recommendations_state_revision_idx ON recommendations(state_revision);

CREATE TABLE model_runs (
    model_run_id TEXT PRIMARY KEY,
    state_revision BIGINT,
    record_json JSONB NOT NULL
);

CREATE INDEX model_runs_state_revision_idx ON model_runs(state_revision);

CREATE TABLE audit_records (
    audit_record_id TEXT PRIMARY KEY,
    record_json JSONB NOT NULL
);

CREATE TABLE checkpoints (
    checkpoint_id TEXT PRIMARY KEY,
    state_revision BIGINT NOT NULL UNIQUE CHECK (state_revision >= 1),
    through_canonical_seq BIGINT NOT NULL CHECK (through_canonical_seq >= 0),
    record_json JSONB NOT NULL
);

CREATE INDEX checkpoints_through_canonical_seq_idx
    ON checkpoints(through_canonical_seq);

-- Shared append-only guard. Any UPDATE or DELETE against a protected table
-- aborts the statement, mirroring the SQLite RAISE(ABORT, ...) triggers. The
-- message names the table (TG_TABLE_NAME) to match the SQLite diagnostics.
CREATE OR REPLACE FUNCTION mosaic_reject_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER raw_events_no_mutation
    BEFORE UPDATE OR DELETE ON raw_events
    FOR EACH ROW EXECUTE FUNCTION mosaic_reject_mutation();

CREATE TRIGGER canonical_events_no_mutation
    BEFORE UPDATE OR DELETE ON canonical_events
    FOR EACH ROW EXECUTE FUNCTION mosaic_reject_mutation();

CREATE TRIGGER canonical_event_incidents_no_mutation
    BEFORE UPDATE OR DELETE ON canonical_event_incidents
    FOR EACH ROW EXECUTE FUNCTION mosaic_reject_mutation();

CREATE TRIGGER canonical_projection_receipts_no_mutation
    BEFORE UPDATE OR DELETE ON canonical_projection_receipts
    FOR EACH ROW EXECUTE FUNCTION mosaic_reject_mutation();

CREATE TRIGGER luna_results_no_mutation
    BEFORE UPDATE OR DELETE ON luna_results
    FOR EACH ROW EXECUTE FUNCTION mosaic_reject_mutation();

CREATE TRIGGER insights_no_mutation
    BEFORE UPDATE OR DELETE ON insights
    FOR EACH ROW EXECUTE FUNCTION mosaic_reject_mutation();

CREATE TRIGGER recommendations_no_mutation
    BEFORE UPDATE OR DELETE ON recommendations
    FOR EACH ROW EXECUTE FUNCTION mosaic_reject_mutation();

CREATE TRIGGER model_runs_no_mutation
    BEFORE UPDATE OR DELETE ON model_runs
    FOR EACH ROW EXECUTE FUNCTION mosaic_reject_mutation();

CREATE TRIGGER audit_records_no_mutation
    BEFORE UPDATE OR DELETE ON audit_records
    FOR EACH ROW EXECUTE FUNCTION mosaic_reject_mutation();

CREATE TRIGGER checkpoints_no_mutation
    BEFORE UPDATE OR DELETE ON checkpoints
    FOR EACH ROW EXECUTE FUNCTION mosaic_reject_mutation();
