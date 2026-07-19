# RFC-0001: Mosaic Demo Foundation — Auditable Event-to-COP Pipeline

- **Status:** Implemented — reconciled through the public operations increment
- **Owner:** Mosaic coordinator
- **Decision date:** 2026-07-18
- **Implementation snapshot:** P01–P20 integrated; public operations API/UI and Docker/E2E acceptance are complete
- **Supersedes:** Nothing
- **Input:** [Mosaic Architecture and Technical Specification](../../Mosaic_Architecture_and_Technical_Specification.md)

## 1. Decision and scope

Mosaic v0.1 is a **local, synthetic, single-process demonstration foundation**.
Its durable and reviewable path is:

```text
Raw Event → Canonical Event → deterministic COP projection
          → evidence-resolvable record → immutable audit record
```

The intended product loop also includes evidence-backed Terra Insights and
supervisor-reviewed Sol Recommendations. The structured Terra and Sol services
are implemented as independently testable adapters, but no executable
composition currently invokes a live model or connects that loop to the HTTP
surface. The binding statements in this RFC distinguish integrated behavior
from deferred production decisions.

Mosaic never dispatches a resource, changes an external operational record,
contacts a person, or makes an operational decision. The demo contains only
synthetic data.

### Status at a glance

| Status | Scope |
|---|---|
| **Integrated** | P01–P20: schemas/contracts and generated mocks, local SQLite store, ingestion, deterministic projection/replay, fixture simulator, public HTTP/SSE and bounded operations surfaces, Svelte UI, structured Terra/Sol services, offline data generation, executable composition, and fresh-local Docker/E2E acceptance. |
| **Future** | Hosted Cloud Run/Cloud SQL, a shared broker, multi-instance coordination, production identity/privacy/retention, live operational integrations, live model-network adapters, and Experience Store retrieval. |

## 2. Binding v0.1 behavior that is implemented

### 2.1 Ontology, records, and local storage

- Authored JSON Schemas in `ontology/` are the source of truth. Checked-in Go
  types are generated in `internal/ontology/gen/`; cross-package seams are in
  `internal/contracts/`.
- The integrated store is **SQLite only**, for the local demo. It persists Raw
  Events, Canonical Events, Luna Results, Insights, Recommendations, Model
  Runs, Audit Records, checkpoints, and projection receipts as append-only
  records. It assigns the durable `canonical_seq` used for projection and
  replay.
- Raw source data is retained as a valid envelope even when the source body is
  malformed. The envelope stores opaque payload bytes, content type, checksum,
  source identity, source record or idempotency key, and receipt metadata.
- Canonical records, model artifacts, and audit records are immutable. A
  correction appends a Canonical Event that identifies the superseded event;
  the effective correction chain is selected from canonical sequence order.
- PostgreSQL is **not** part of v0.1 and is never bundled into the application
  container. A future hosted demo may use external Cloud SQL for PostgreSQL
  behind the same store contract.

### 2.2 Ingestion, ordering, projection, and recovery

- P05 persists the Raw Event before invoking its injected Luna normalizer.
  `(source_id, source_record_id)` is idempotent when a source record ID exists;
  otherwise the source must supply an idempotency key. An exact re-delivery
  returns the existing lifecycle result without a second Canonical Event or
  state revision.
- Luna results are schema-validated and have one of `accepted`, `repaired`,
  `quarantined`, or `rejected`. Only accepted and repaired results carry a
  projectable Canonical Event. Quarantined and rejected inputs remain durable
  but do not change the COP.
- Canonical append order—not `occurred_at`—is deterministic replay order. Late
  delivery is consequently appended at a later `canonical_seq`. Corrections
  keep their antecedents in history and recompute the affected deterministic
  projection from effective canonical records.
- P06 is the only source-derived COP mutator. Its apply operation writes a
  projection receipt and checkpoint together in its own SQLite transaction;
  retrying an already projected event does not create another revision or
  checkpoint. Recovery loads the latest checkpoint and replays later Canonical
  Events to reproduce the serialized COP and revision.

#### Current persistence/dispatch boundary

The v0.1 foundation does **not** have one atomic transaction from Canonical
Event persistence through projection and dispatch. Its implemented sequence is:

1. Persist the Raw Event.
2. Persist the Luna Model Run.
3. In one transaction, append the Canonical Event and its Luna Result.
4. After that transaction commits, invoke the in-process deterministic
   dispatcher for the committed Canonical Event.
5. The projector independently commits its receipt and checkpoint transaction.

If post-commit dispatch fails, the durable Canonical Event remains and P05
returns that condition as `DispatchError`; it is not rolled back. The fixture
simulator stops that run on a dispatch error, and the replay runner can rebuild
a deterministic COP from the stored log. There is not yet a composed background
worker, durable outbox, or automatic retry policy. P14 seeds and recovers the
frozen scenario during local startup, but it does not add a background recovery
worker; a later production RFC must decide shared-broker and multi-instance
recovery semantics.

### 2.3 Deterministic fixture scenario

P04/P07 provide the frozen `domestic-disturbance` fixture and a CLI simulator.
The ten declared raw-event beats cover the product-story call, welfare context,
weather, road state, EMS availability, officer update, a repairable incomplete
road event, a quarantined invalid event, late EMS delivery, and a road-opening
correction. The fixture run proves a final state revision of 9 and checkpoint
recovery of the same COP.

Expected Insights and Recommendations are validated fixture artifacts for the
Terra and Sol contracts. The P07 simulator does not run Terra or Sol as part of
its deterministic event loop.

### 2.4 Structured model boundaries and auditability

- P10 and P11 accept injected, least-privilege structured clients. Terra sees
  serialized committed COP data, a state revision, and permitted evidence;
  Sol additionally sees active Insights and retains a fixture-level
  `supervisor-demo` requester guard. It is not composed into the public HTTP
  surface; a live advisory integration must revisit that boundary.
- Candidate Insights and Recommendations are schema-validated, constrained to
  their requested state revision and permitted evidence, and persist a
  ModelRun containing provider/model/prompt/schema identity, inputs, outputs,
  timestamps, response metadata, and validation/failure status.
- Refusals, invalid structured output, client failures, and timeouts persist a
  ModelRun but create no Insight or Recommendation and cannot mutate the COP.
  Insight obsolescence is represented by an appended record, not by deleting
  history.
- These services intentionally construct **no network client, OpenAI client,
  API server, tool, shell, or operational-action client**. They are exercised
  with fixture clients. A live model-network adapter and a policy for composing
  it are not implemented in v0.1.

The evidence, validation, canonical ordering, projection, checkpoints, and
replay process are deterministic from accepted persisted artifacts. Model
inference is not assumed deterministic; provenance records make an accepted
model result auditable rather than reproducible by re-querying a model.

### 2.5 Current public /api/v1 surface and pluggable access policy

P17 retains a local HTTP/SSE **read and immutable-audit-record surface**, not an
event-ingest, scenario-control, live-agent, or operational-action API. The
injected API-local `PublicActorResolver` resolves every caller to `public-demo`,
and `AllowDemoPolicy` permits the current demo routes. They are composition
seams for a later identity-aware resolver and policy; no login, token, session,
or configured access restriction exists in this demo.

`X-Mosaic-Demo-Identity` is optional display metadata only. It is not a
credential and never gates access. A no-header audit record uses the
schema-valid `public-demo` / `viewer` display identity.

| Route | Implemented behavior |
|---|---|
| GET /api/v1/health | Returns local health. |
| GET /api/v1/version | Returns the API version. |
| GET /api/v1/cop | Returns a recovered deterministic COP and its revision. |
| GET /api/v1/evidence/{kind}/{id} | Resolves a state fact or one persisted evidence target. |
| GET /api/v1/artifacts/{kind}/{id} | Resolves one persisted immutable artifact. |
| GET /api/v1/stream | Sends an initial `cop.snapshot`, then best-effort locally published named events. |
| GET /api/v1/operations | Returns bounded aggregate record/lifecycle/model-run counts, same-request recovery facts, and local-stream metadata; it never returns raw payloads, checksums, prompts, or model responses. |
| POST /api/v1/briefings | Appends a `briefing_requested` Audit Record and responds with `executed: false`. It does not invoke Sol. |
| POST /api/v1/audit-actions | Validates an existing Insight or Recommendation target, appends an acknowledgement/rejection/note Audit Record, and responds with `executed: false`. |

There is currently no /events, /insights, /scenarios/{id}/run, or
Recommendation-producing HTTP endpoint. Apart from the initial `cop.snapshot`,
event names and publishers are composition concerns. The broker remains
process-local and best-effort; it is not shared multi-instance notification.

### 2.6 Dashboard and operations receipt

P09/P18 is a Svelte 5 runes application on Vite 8. It provides a deliberately
local evidence-aware COP ledger and a public operations receipt. It has no
identity chooser or browser auth header: public review controls append only
immutable records with `executed: false`.

The receipt presents the observed/source timestamps, recovered COP revision,
bounded durable and lifecycle counts, persisted model-run outcomes, local SSE
metadata, and capability mode/status statements. It visibly distinguishes
fixture, composed, recovered, degraded, and unavailable conditions. It names
live Terra/Sol transport, durable reconciliation, and external operational
action as unavailable rather than implying they run.

The UI displays reported facts from the COP and does not infer a derived
assessment from them. It leaves assessment/recommendation display unavailable
until an evidence-resolvable API record is composed. It also omits
`payload_bytes_b64` and `raw_sha256` when rendering an evidence artifact:
raw source payload is not rendered by default.

### 2.7 Offline synthetic-data production

P13 adds an offline `cmd/datasetgen` process for the selected Gemma GGUF via an
explicit local `llama.cpp` executable. The GGUF and staging directory belong in
ignored `localmodels/`; the generator has no downloader, network code,
credentials, or runtime role.

Generation writes only a new empty stage with the raw model response, strict
artifact bundle, and provenance (model/executable/prompt identities and hashes,
schema versions, bounded arguments, seed, and response checksums). A reviewed
candidate is admitted only by explicit `freeze` into a new versioned
`datasets/` child after provenance, checksum, schema, reference, and artifact
validation. The checked-in frozen fixture—not repeated inference—is the demo
input.

## 3. Executable composition and Docker acceptance

| Parcel | Status and acceptance boundary | Current limitation |
|---|---|---|
| **P14/P19 — executable composition** | **Integrated.** `cmd/mosaicdemo` validates the frozen dataset, opens SQLite, seeds/replays the deterministic scenario, composes the public API and SQLite operations reader, and hosts the prebuilt dashboard. | The executable requires a separately prebuilt `ui/dist`; no dashboard build artifact is committed. It does not compose Terra/Sol or publish a live-model assessment stream. |
| **P12/P20 — Docker and public acceptance** | **Integrated.** The multi-stage image builds the Vite dashboard and `mosaicdemo`; Compose initializes only the named SQLite volume, then runs the application nonroot and read-only. The E2E suite and fresh Docker smoke prove no-header dashboard delivery, COP revision 9, governed evidence resolution, bounded operations telemetry, SSE, and immutable `executed: false` audit behavior. | The standard image intentionally contains no GGUF, local model directory, live model client, reconciliation worker, PostgreSQL service, or operational-system client. |

P10/P11 remain valid structured service parcels, but their live invocation,
fixture composition, artifact read exposure, and any automatic Terra trigger
remain outside `mosaicdemo`. A future parcel or RFC must make that integration
decision explicitly.

## 4. Acceptance evidence available now

The integrated package tests prove the following narrow boundaries:

| Area | Current proof |
|---|---|
| Schemas/contracts | Schema compilation, checked-in generated type verification, fixture validation, and cross-package contracts. |
| Store and ingestion | SQLite migrations, immutable append behavior, canonical sequence, idempotency, Luna lifecycle provenance, and post-commit dispatch error handling. |
| Deterministic state | Canonical ordering, correction handling, idempotent projector retry, checkpoint rollback behavior, restart replay, and byte-identical fixture COP serialization. |
| Scenario | Ten-beat fixture replay, repaired/quarantined/late/correction paths, final revision 9, and replay verification. |
| Terra/Sol | Structured fixture clients; schema, evidence, revision, role, neutral-language, refusal/failure/timeout, lifecycle, and ModelRun-record checks. |
| API/UI | Public no-header HTTP/SSE, bounded operations telemetry, immutable audit endpoint, and static-host tests; P19 composition tests seed the deterministic API and operations reader. Svelte 5/Vite 8 checks are provided by the UI package. |
| Dataset generation | Local runner, staged bundle/provenance, validation, explicit freeze, and destination-safety tests. |

Latency and throughput are observed only; they are not v0.1 release gates. The
P14 local executable composition is covered by package tests; P12 adds the fresh Docker build/start/smoke and durable-volume acceptance boundary.

## 5. Deferred production decisions

The following are intentionally not v0.1 behavior and require a later RFC or
ADR before implementation:

- Cloud Run deployment with external Cloud SQL for PostgreSQL, database
  migration strategy, backups, RPO/RTO, and connection management.
- Shared broker/outbox, cross-instance ordering, leases, redelivery, and
  automatic recovery/retry of committed but unprojected Canonical Events.
- Production identity, authorization, audit tamper resistance, privacy,
  retention, secrets management, and real-data controls.
- Live OpenAI or other model transport, model selection/configuration, prompt
  rollout, operational monitoring, and end-to-end safety evaluation.
- CAD/RMS/radio/weather connectors, real operational data, multi-tenant or
  high-availability behavior, and latency/throughput release objectives.
- Experience Store retrieval, eligibility, bias review, and causal-language
  restrictions.

## 6. Non-negotiable safety boundary

For every later parcel, Raw Events, Canonical Events, Insights,
Recommendations, Model Runs, and Audit Records remain immutable; corrections
and obsolescence append new records. The deterministic projector remains the
only mutator of source-derived COP state. AI output may assess evidence-backed
state but cannot execute an operational action, and supervisor HTTP endpoints
continue to create immutable records with `executed: false` only.
