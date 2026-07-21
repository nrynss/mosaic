-- Event spine transport tables (parcel A2).
--
-- These are the Postgres-side durable log and consumer-cursor store that the
-- pluggable EventLog / EventConsumer implementations (parcels B2 / B3) write
-- against. They are intentionally separate from the append-only provenance
-- and read-model tables in 0001: that store is the system of record forever;
-- this log is the transport layer that a future Kafka/Redpanda backend can
-- replace without touching the read model.
--
-- Semantics (see internal/eventlog and HANDOFF-v0.4 §2.3–2.6):
--   * partition_key is the routing and per-key ordering unit (default: incident
--     id). Global sequence is monotonic; the subsequence for one key ordered by
--     sequence is the per-partition delivery order.
--   * idempotency_key UNIQUE makes at-least-once Append safe: a re-append of an
--     already-seen key is a no-op conflict that B2 turns into success.
--   * event_consumer_checkpoints holds the opaque position token that B3
--     advances atomically with projection (project+position, never append+project).
--   * event_log is append-only; checkpoints are mutable (UPSERT on advance).

CREATE TABLE event_log (
    sequence BIGSERIAL PRIMARY KEY,
    partition_key TEXT NOT NULL,
    idempotency_key TEXT NOT NULL UNIQUE,
    event_type TEXT NOT NULL,
    payload BYTEA NOT NULL,
    appended_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Supports per-partition ordered consume:
--   SELECT ... FROM event_log
--   WHERE partition_key = $1 AND sequence > $2
--   ORDER BY sequence ASC
CREATE INDEX event_log_partition_sequence_idx
    ON event_log (partition_key, sequence);

-- Mutable cursor store: one position token per (consumer_group, partition_key).
-- position_token is the opaque eventlog.Position.Token() string; for this
-- Postgres backend it is the decimal encoding of event_log.sequence (see
-- EncodeSequenceToken / DecodeSequenceToken in event_spine.go).
CREATE TABLE event_consumer_checkpoints (
    consumer_group TEXT NOT NULL,
    partition_key TEXT NOT NULL,
    position_token TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer_group, partition_key)
);

-- The transport log is immutable history. Consumer checkpoints deliberately
-- do NOT get this trigger — B3 must UPSERT position_token on each ack.
CREATE TRIGGER event_log_no_mutation
    BEFORE UPDATE OR DELETE ON event_log
    FOR EACH ROW EXECUTE FUNCTION mosaic_reject_mutation();
