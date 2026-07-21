-- Simulation session epochs (parcel C3).
--
-- Application-level session metadata and an optional durable active-session
-- pointer. Ontology JSON schemas are intentionally unchanged: session scoping
-- for advisories in the demo path uses an in-memory SessionAdvisoryView (see
-- internal/api) rather than adding session_id columns to insight/recommendation
-- records.
--
-- The in-process ActiveSession holder remains the authoritative pointer for a
-- single app instance (SQLite and typical local Postgres). The tables below
-- support multi-instance / restart durability when composition mirrors Set/Clear
-- into them; they are not required for the single-process demo path.

CREATE TABLE session_epoch (
    session_id TEXT PRIMARY KEY,
    status TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (status IN ('pending', 'running', 'ended')),
    CHECK (ended_at IS NULL OR ended_at >= started_at)
);

CREATE INDEX session_epoch_status_idx ON session_epoch (status);
CREATE INDEX session_epoch_started_at_idx ON session_epoch (started_at DESC);

-- Singleton active-session pointer (at most one row, singleton = 1).
CREATE TABLE active_session_pointer (
    singleton SMALLINT PRIMARY KEY DEFAULT 1 CHECK (singleton = 1),
    session_id TEXT REFERENCES session_epoch (session_id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Session-scoped advisory index (demo durable path). Maps session epochs to
-- insight / recommendation / model_run ids without rewriting ontology tables.
-- The in-memory SessionAdvisoryView is preferred for unit tests and SQLite;
-- this table is the Postgres durable equivalent when composition records ids.
CREATE TABLE session_advisory_index (
    session_id TEXT NOT NULL REFERENCES session_epoch (session_id) ON DELETE CASCADE,
    record_kind TEXT NOT NULL,
    record_id TEXT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, record_kind, record_id),
    CHECK (record_kind IN ('insight', 'recommendation', 'model_run', 'audit_record'))
);

CREATE INDEX session_advisory_index_kind_idx
    ON session_advisory_index (session_id, record_kind);
