-- Materialized COP read-model (parcel B5).
--
-- This is the system-of-record *read model* layer for cheap GET /cop. It is
-- separate from:
--   * event_log (transport; replaceable by Kafka/Redpanda later)
--   * checkpoints (append-only recovery snapshots for deterministic rebuild)
--
-- The projector (via MaterializingProjector) UPSERTs the active row whenever
-- projection advances. Until C3 introduces session_id epochs, a single stable
-- key ("default") holds the active COP. Multiple keys remain valid for later
-- session isolation without a schema change.
--
-- Unlike provenance tables this row is intentionally mutable: each successful
-- projection replaces the previous JSON snapshot for its key.

CREATE TABLE cop_read_model (
    read_model_key TEXT PRIMARY KEY,
    state_revision BIGINT NOT NULL,
    projected_at TIMESTAMPTZ NOT NULL,
    cop_json JSONB NOT NULL,
    -- Full gen.Checkpoint payload for diagnostics and optional rebuild aids.
    checkpoint_json JSONB,
    through_canonical_seq BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (state_revision >= 0),
    CHECK (through_canonical_seq IS NULL OR through_canonical_seq >= 0)
);

CREATE INDEX cop_read_model_state_revision_idx
    ON cop_read_model (state_revision);
