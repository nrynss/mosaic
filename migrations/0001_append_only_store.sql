CREATE TABLE IF NOT EXISTS schema_migrations (
    migration_name TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL
);

CREATE TABLE raw_events (
    raw_event_id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL,
    source_record_id TEXT,
    idempotency_key TEXT,
    record_json TEXT NOT NULL CHECK (json_valid(record_json))
);

CREATE UNIQUE INDEX raw_events_source_record_id_unique
    ON raw_events(source_id, source_record_id)
    WHERE source_record_id IS NOT NULL;

CREATE UNIQUE INDEX raw_events_idempotency_key_unique
    ON raw_events(source_id, idempotency_key)
    WHERE source_record_id IS NULL AND idempotency_key IS NOT NULL;

CREATE TABLE canonical_events (
    canonical_seq INTEGER PRIMARY KEY AUTOINCREMENT,
    canonical_event_id TEXT NOT NULL UNIQUE,
    raw_event_id TEXT NOT NULL REFERENCES raw_events(raw_event_id),
    supersedes_event_id TEXT REFERENCES canonical_events(canonical_event_id),
    record_json TEXT NOT NULL CHECK (json_valid(record_json)),
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
    state_revision INTEGER NOT NULL CHECK (state_revision >= 1),
    recorded_at TEXT NOT NULL
);

CREATE TABLE luna_results (
    luna_result_id TEXT PRIMARY KEY,
    raw_event_id TEXT NOT NULL REFERENCES raw_events(raw_event_id),
    canonical_event_id TEXT REFERENCES canonical_events(canonical_event_id),
    record_json TEXT NOT NULL CHECK (json_valid(record_json))
);

CREATE INDEX luna_results_raw_event_id_idx ON luna_results(raw_event_id);

CREATE TABLE insights (
    insight_id TEXT PRIMARY KEY,
    state_revision INTEGER NOT NULL CHECK (state_revision >= 1),
    record_json TEXT NOT NULL CHECK (json_valid(record_json))
);

CREATE INDEX insights_state_revision_idx ON insights(state_revision);

CREATE TABLE recommendations (
    recommendation_id TEXT PRIMARY KEY,
    state_revision INTEGER NOT NULL CHECK (state_revision >= 1),
    record_json TEXT NOT NULL CHECK (json_valid(record_json))
);

CREATE INDEX recommendations_state_revision_idx ON recommendations(state_revision);

CREATE TABLE model_runs (
    model_run_id TEXT PRIMARY KEY,
    state_revision INTEGER,
    record_json TEXT NOT NULL CHECK (json_valid(record_json))
);

CREATE INDEX model_runs_state_revision_idx ON model_runs(state_revision);

CREATE TABLE audit_records (
    audit_record_id TEXT PRIMARY KEY,
    record_json TEXT NOT NULL CHECK (json_valid(record_json))
);

CREATE TABLE checkpoints (
    checkpoint_id TEXT PRIMARY KEY,
    state_revision INTEGER NOT NULL UNIQUE CHECK (state_revision >= 1),
    through_canonical_seq INTEGER NOT NULL CHECK (through_canonical_seq >= 0),
    record_json TEXT NOT NULL CHECK (json_valid(record_json))
);

CREATE INDEX checkpoints_through_canonical_seq_idx
    ON checkpoints(through_canonical_seq);

CREATE TRIGGER raw_events_no_update
BEFORE UPDATE ON raw_events
BEGIN
    SELECT RAISE(ABORT, 'raw_events is append-only');
END;

CREATE TRIGGER raw_events_no_delete
BEFORE DELETE ON raw_events
BEGIN
    SELECT RAISE(ABORT, 'raw_events is append-only');
END;

CREATE TRIGGER canonical_events_no_update
BEFORE UPDATE ON canonical_events
BEGIN
    SELECT RAISE(ABORT, 'canonical_events is append-only');
END;

CREATE TRIGGER canonical_events_no_delete
BEFORE DELETE ON canonical_events
BEGIN
    SELECT RAISE(ABORT, 'canonical_events is append-only');
END;

CREATE TRIGGER canonical_event_incidents_no_update
BEFORE UPDATE ON canonical_event_incidents
BEGIN
    SELECT RAISE(ABORT, 'canonical_event_incidents is append-only');
END;

CREATE TRIGGER canonical_event_incidents_no_delete
BEFORE DELETE ON canonical_event_incidents
BEGIN
    SELECT RAISE(ABORT, 'canonical_event_incidents is append-only');
END;

CREATE TRIGGER canonical_projection_receipts_no_update
BEFORE UPDATE ON canonical_projection_receipts
BEGIN
    SELECT RAISE(ABORT, 'canonical_projection_receipts is append-only');
END;

CREATE TRIGGER canonical_projection_receipts_no_delete
BEFORE DELETE ON canonical_projection_receipts
BEGIN
    SELECT RAISE(ABORT, 'canonical_projection_receipts is append-only');
END;

CREATE TRIGGER luna_results_no_update
BEFORE UPDATE ON luna_results
BEGIN
    SELECT RAISE(ABORT, 'luna_results is append-only');
END;

CREATE TRIGGER luna_results_no_delete
BEFORE DELETE ON luna_results
BEGIN
    SELECT RAISE(ABORT, 'luna_results is append-only');
END;

CREATE TRIGGER insights_no_update
BEFORE UPDATE ON insights
BEGIN
    SELECT RAISE(ABORT, 'insights is append-only');
END;

CREATE TRIGGER insights_no_delete
BEFORE DELETE ON insights
BEGIN
    SELECT RAISE(ABORT, 'insights is append-only');
END;

CREATE TRIGGER recommendations_no_update
BEFORE UPDATE ON recommendations
BEGIN
    SELECT RAISE(ABORT, 'recommendations is append-only');
END;

CREATE TRIGGER recommendations_no_delete
BEFORE DELETE ON recommendations
BEGIN
    SELECT RAISE(ABORT, 'recommendations is append-only');
END;

CREATE TRIGGER model_runs_no_update
BEFORE UPDATE ON model_runs
BEGIN
    SELECT RAISE(ABORT, 'model_runs is append-only');
END;

CREATE TRIGGER model_runs_no_delete
BEFORE DELETE ON model_runs
BEGIN
    SELECT RAISE(ABORT, 'model_runs is append-only');
END;

CREATE TRIGGER audit_records_no_update
BEFORE UPDATE ON audit_records
BEGIN
    SELECT RAISE(ABORT, 'audit_records is append-only');
END;

CREATE TRIGGER audit_records_no_delete
BEFORE DELETE ON audit_records
BEGIN
    SELECT RAISE(ABORT, 'audit_records is append-only');
END;

CREATE TRIGGER checkpoints_no_update
BEFORE UPDATE ON checkpoints
BEGIN
    SELECT RAISE(ABORT, 'checkpoints is append-only');
END;

CREATE TRIGGER checkpoints_no_delete
BEFORE DELETE ON checkpoints
BEGIN
    SELECT RAISE(ABORT, 'checkpoints is append-only');
END;
