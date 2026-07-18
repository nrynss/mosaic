# RFC-0001: Mosaic Demo Foundation — Auditable Event-to-COP Pipeline

- **Status:** Draft — build contract for v0.1
- **Owner:** Mosaic coordinator
- **Decision date:** 2026-07-18
- **Supersedes:** Nothing
- **Input:** [`Mosaic_Architecture_and_Technical_Specification.md`](../../Mosaic_Architecture_and_Technical_Specification.md)

## 1. Summary

Mosaic v0.1 is a local, single-instance Docker demonstration of one synthetic,
replayable domestic-disturbance scenario. It proves an evidence-backed human
decision-support loop:

```text
Raw Event → Canonical Event → deterministic COP projection
          → evidence-backed Insight → supervisor-reviewed Recommendation
          → append-only Audit Record
```

The demo never dispatches a resource, changes an operational record, contacts a
person, or otherwise makes an operational decision. AI informs; humans decide.

## 2. Goals and non-goals

### Goals

- Demonstrate continuous information fusion without prompting.
- Make every displayed claim traceable to persisted evidence and a state revision.
- Preserve raw inputs, normalized revisions, corrections, model runs, and human
  decisions as immutable records.
- Rebuild an identical serialized COP from a checkpoint plus Canonical Events.
- Provide a small Svelte dashboard for fixed synthetic-demo viewer and supervisor
  identities.
- Keep the online AI path structurally constrained and fail-safe.
- Generate synthetic datasets offline with Gemma 4 E2B GGUF through llama.cpp.

### Non-goals

- Production CAD/RMS/radio/weather integration, real operational data, or PII.
- Multi-instance coordination, HA, throughput targets, a shared broker, or a
  Cloud Run deployment. The interfaces must leave room for them.
- Experience Store retrieval, model training, fine-tuning, risk prediction, or
  autonomous action.
- A broad city ontology beyond the scenario's incident, unit/resource, road,
  weather, location, evidence, insight, recommendation, and audit needs.
- Enterprise authentication, RBAC, retention automation, or tamper-proof audit
  guarantees. The demo has only the fixed roles defined below.

## 3. Accepted decisions

1. **Synthetic and local first.** SQLite is the durable system of record for the
   local/rehearsal demo. PostgreSQL is not embedded in the application container.
   A hosted Cloud Run demo will later use the same store contract with external
   Cloud SQL for PostgreSQL.
2. **One process, durable history.** The v0.1 runtime has one application
   instance, an in-process dispatcher, and an in-memory COP projection backed by
   SQLite events and checkpoints.
3. **Schemas are source of truth.** Authored schemas live in `ontology/`; checked-
   in generated Go types live in `internal/ontology/gen/`; cross-package
   interfaces live in `internal/contracts/`.
4. **Facts and assessments are distinct.** A Raw Event records an observation;
   a Canonical Event is a validated normalized revision; an Insight is a derived
   assessment; a Recommendation is a human-review-only option.
5. **History is immutable.** Corrections, recoveries, obsolescence, and human
   decisions append new records with references rather than altering stored rows.
6. **The projector is deterministic; model inference is not.** Validation,
   append order, projection, checkpoints, and replay are reproducible from stored
   accepted artifacts. Model requests and outputs are recorded for audit, not
   expected to re-run identically.
7. **AI produces structured output only.** Luna, Terra, and Sol use versioned
   JSON-Schema response contracts. For OpenAI Responses API adapters, use strict
   Structured Outputs through `text.format`; application-side validation and
   refusal handling remain mandatory.
8. **Offline generation is separate.** The Gemma GGUF model runs through
   llama.cpp in `cmd/datasetgen`, never in the online application image or live
   Luna/Terra/Sol path. Model artifacts are not committed.

## 4. Architecture

```text
Simulator / synthetic source
  → Raw Event store
  → Luna result (accepted | repaired | quarantined | rejected)
  → Canonical Event append log
  → deterministic COP projector + checkpoint
  → Terra Insight lifecycle
  → Sol briefing on supervisor request
  → Dashboard + append-only human Audit Record
```

The Canonical Event log is the input to the COP projector and Terra. Sol receives
only structured COP state, Insights, and Evidence; it never receives arbitrary
source text. Luna may inspect raw source text only to produce a Canonical Event
or a non-mutating result.

## 5. Ontology and schema rules

### 5.1 Versioning and generated code

Every schema has an immutable `$id` and semantic `schema_version`. Compatible,
additive changes increment the minor version. Breaking changes require a new
major version and an adapter or migration. The schema gate must fail if generated
Go code is out of date, a fixture fails validation, or a reference is invalid.

### 5.2 Record contracts

The first schema set contains:

- `raw-event.schema.json`
- `canonical-event.schema.json`
- `luna-result.schema.json`
- `incident.schema.json`, `unit.schema.json`, `resource.schema.json`
- `location.schema.json`, `road.schema.json`, `weather.schema.json`
- `evidence.schema.json`, `insight.schema.json`, `recommendation.schema.json`
- `model-run.schema.json`, `audit-record.schema.json`, `checkpoint.schema.json`
- `scenario.schema.json`, `dataset-manifest.schema.json`

The Raw Event envelope must be valid even when its source payload is malformed.
It carries an opaque `payload_bytes_b64`, `content_type`, `raw_sha256`, source
identity, optional source record ID, optional source occurrence time, and receipt
time. The unparseable payload is preserved unchanged.

A Canonical Event has its own ID and append sequence, references one Raw Event,
and includes type, schema version, UTC occurrence/receipt times, typed payload,
entity/incident references, provenance, confidence dimensions, and optional
`supersedes_event_id`.

Luna returns exactly one `LunaResult` status:

- `accepted` — Canonical Event is valid with no recovered field;
- `repaired` — Canonical Event includes original/replacement values, repair
  method, evidence, and confidence;
- `quarantined` — stored for review but not projectable; or
- `rejected` — stored as a rejected source envelope with reason.

## 6. Event lifecycle and projection semantics

### 6.1 Identity, idempotency, and ordering

`(source, source_record_id)` is the idempotency key when a source record ID is
available. A repeated key returns the original result and creates neither a new
Raw Event nor a state revision. Sources without an ID use a caller-provided
idempotency key; they are not semantically merged automatically.

Each persisted Canonical Event receives a database-assigned, monotonically
increasing `canonical_seq`. Projection and replay order are ascending
`canonical_seq`; `occurred_at` is domain data, not a replay sort key. This makes
late delivery deterministic: a late event is appended, then recomputes the
affected incident projection at the next state revision.

An exact duplicate is handled by the idempotency key. A suspected semantic
duplicate remains a separate record with a confidence-scored `duplicate_of`
relationship and evidence. It is visible in the UI and does not erase either
source observation.

### 6.2 Corrections and effective events

A correction is a new Canonical Event that names `supersedes_event_id` and a
reason. A superseded event remains in the log but is not effective in a fresh
projection. If multiple direct corrections exist, the highest `canonical_seq` is
effective. The projector recomputes only the affected incident from the durable
log's effective events, then writes the next global state revision.

### 6.3 Transaction and recovery boundary

Raw Event persistence is its own durable action. Creating a Canonical Event,
marking its projection status, calculating the next deterministic COP revision,
and storing the resulting checkpoint happen in one SQLite transaction. External
model calls never occur inside that transaction.

After commit, the dispatcher invokes Terra. Insight and Recommendation writes are
separate append-only transactions tied to the committed `state_revision`. On
restart, Mosaic loads the latest checkpoint and replays later Canonical Events;
it also finds committed, unprojected Canonical Events and completes projection.
No outbox is required for v0.1.

## 7. Minimum COP projection

The COP is a versioned, serializable read model with:

- `state_revision`, projection timestamp, and effective event IDs;
- Incidents with `open` or `resolved` status, location, linked entities, and
  event history;
- Unit/resource availability (`available`, `assigned`, `unavailable`);
- Roads (`open` or `blocked`) and the effective supporting event;
- Weather alerts (`active` or `cleared`);
- Typed, confidence-scored entity/incident associations; and
- Current active Insights by ID, with their lifecycle status held separately from
  source-derived facts.

Only the projector changes these source-derived fields. Terra may append Insights
and relation assessments but cannot rewrite Canonical Event facts or send commands.

## 8. Evidence, confidence, and AI contracts

`EvidenceRef` contains a target kind (`raw_event`, `canonical_event`,
`state_fact`, or `insight`), target ID, optional JSON Pointer, and explanation.
Every material Insight assertion must cite one or more persisted EvidenceRefs.

Confidence is an evidence-strength assessment, not an outcome probability or
permission to act. It is an object with `source_quality`,
`transformation_certainty`, and `reasoning_support`, each `low`, `medium`, or
`high` plus a short basis. Luna normally supplies the first two; Terra supplies
reasoning support; Sol displays relevant dimensions but does not invent a
probability.

Each model invocation writes a `ModelRun`: provider/model identifier, prompt
version, schema version, input event IDs, state revision, output IDs, validation
result, response ID where available, timestamps, and failure/refusal details.

For OpenAI adapters, strict Structured Outputs constrain the response shape, but
the application still performs schema, evidence, and policy validation. A model
refusal, invalid output, timeout, or provider failure creates a failed ModelRun
and emits "no AI assessment available"; it never prevents Canonical Event
projection or modifies operational state.

Luna, Terra, and Sol must treat untrusted text as data. It cannot alter schemas,
workflow routing, tool permissions, or policy. v0.1 gives these adapters no tools
that can modify an external system.

## 9. API, SSE, and dashboard contract

All v0.1 routes are versioned under `/api/v1`.

| Route | Purpose |
|---|---|
| `POST /events` | Persist a Raw Event and return its lifecycle status. |
| `GET /events` | Read event, correction, and recovery history. |
| `GET /cop` | Return the current serialized COP and state revision. |
| `GET /insights` | Return active and obsolete Insights with evidence. |
| `POST /scenarios/{id}/run` | Run a versioned synthetic scenario. |
| `POST /briefings` | Supervisor-only Sol briefing request. |
| `POST /audit-actions` | Record supervisor acknowledgement, rejection, or note. |
| `GET /stream` | SSE stream of read-model changes. |

SSE types are `cop.snapshot`, `event.lifecycle`, `insight.created`,
`insight.obsolete`, `recommendation.created`, `audit.created`, and
`system.status`.

The demo supports two fixed identities passed by a local demo header or control:
`viewer-demo` and `supervisor-demo`. Viewers may inspect data. Only the
supervisor may request a briefing or record an action. This is a demonstration
constraint, not production authentication.

The UI labels every display item as **Reported Fact**, **Derived Assessment**,
or **Human-review Recommendation**, and links it to its evidence and state
revision. A Recommendation uses neutral language such as “consider”, “review”,
or “verify”; it cannot dispatch, mutate an incident, contact a person, or use
imperative command language.

## 10. Synthetic data and scenario

`cmd/datasetgen` invokes llama.cpp against the configured local Gemma model path.
The model artifact is
`unsloth/gemma-4-E2B-it-GGUF/gemma-4-E2B-it-UD-Q8_K_XL.gguf`. It is acquired
locally (for example, with `hf download`), kept in ignored `models/`, and never
included in the application image. The generator records a manifest with model,
prompt, schema versions, deterministic ID map, and seed where supported. Only
validated artifacts enter `datasets/`.

The v0.1 `domestic-disturbance` scenario begins with the six product-story beats:
911 call, historical welfare check, weather alert, road closure, EMS availability,
and officer radio update. Acceptance fixtures add an incomplete road event that
Luna repairs, one invalid event that is quarantined, a late delivery, and a road
reopening correction. Terra must create an access-constraint Insight with road
and weather evidence, then mark it obsolete after the correction. A supervisor
requests Sol after the officer update; the resulting Recommendation and supervisor
action are audited.

## 11. Acceptance criteria

| Area | Required proof |
|---|---|
| Schema/type gate | Schemas validate, generated types are current, and golden fixtures round-trip. |
| Ingestion | Duplicate source delivery changes state once; malformed sources persist without state change. |
| Luna | Accepted, repaired, quarantined, and rejected paths preserve the required provenance. |
| Corrections | Late and superseding events produce the specified revision while retaining history. |
| Replay | Checkpoint plus later events rebuild the identical serialized COP and revision after restart. |
| Terra | Every Insight has valid evidence and state revision; obsolete Insights append notices rather than vanish. |
| Sol | Only a supervisor can request it; output is neutral, schema-valid, evidence-cited, and audited. |
| Failure | Refusal, invalid output, or timeout produces a ModelRun and no state mutation. |
| UI | Every displayed item resolves its evidence and claim type. |
| End-to-end | A fresh local Docker run completes the scenario and full audit trail. |

## 12. Follow-on work

RFC-0002 will decide the hosted Cloud Run/Cloud SQL deployment, durable broker,
multi-instance ordering/leases, backup/recovery objectives, and production
security/privacy controls. Experience retrieval requires its own RFC covering
eligibility, retention, bias review, and causal-language restrictions.
