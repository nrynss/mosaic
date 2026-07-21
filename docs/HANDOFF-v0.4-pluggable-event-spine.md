# Mosaic Handoff v0.4 — Pluggable Event Spine, Durable Persistence, and Simulation Isolation

**Status:** Planned (design locked). Implementation on a dedicated branch; `main`
keeps this plan and a working demo. Not deadline-bound — the objective is a
genuinely good engineering system, not a demo skin.

---

## 1. Why this increment exists

A trace of the v0.3 interactive path surfaced a correctness/honesty gap:

- At startup, `domainRuntime.Run()` ([cmd/mosaicdemo/main.go](../cmd/mosaicdemo/main.go))
  **ingests all 10 beats at once** and seeds the full scenario (COP revision 9)
  plus all advisories into the store **before the server serves a request**.
- "Play scenario" ([internal/simulation/session/controller.go](../internal/simulation/session/controller.go))
  is a **cosmetic SSE overlay** — it emits beat metadata on a timer; the UI
  re-reads a COP that is already final.
- `GET /cop` always runs deterministic recovery over the fully-seeded log
  ([internal/api/server.go](../internal/api/server.go)) → returns revision 9 every
  time. The board goes **blank → final**; the EMS-available→unavailable flip and
  the Brook Lane closed→open correction **never render as live transitions**.
- The live inference routes (`/operator/analyze` → Terra, `/operator/brief` → Sol
  in [internal/api/operator.go](../internal/api/operator.go)) exist but **nothing in
  the UI calls them**. "Refresh advice" only re-reads seeded fixture history.
- Beat delays are **relative to session start**, and the fixture uses
  `[0,100,100,…]`, so beats 2–10 fire at ~100ms — **nine near-simultaneous
  beats**, each triggering `GET /cop` + `GET /advisories` from the UI (~20 reads
  in under a second). That is the API "flood."

The product must work **as envisioned**: the simulation is a *real* event source
driving a *real* pipeline in real time, with genuine inference — not a pre-seeded
board with a timer over it. The dummy *data* stays dummy; the *simulation* does
not.

This increment makes the system event-driven and honestly scalable, with a
persistence and streaming design that is pluggable up to a real log
(Kafka/Redpanda) when — and only when — throughput demands it.

---

## 2. Architectural spine (locked)

### 2.1 Three separable layers

Treat these as distinct from day one, even while all three are physically
Postgres:

| Layer | Responsibility | Now | Later (scale) |
|-------|----------------|-----|---------------|
| **Log (transport)** | Append + ordered consume of events | Postgres (`SKIP LOCKED` + sequence) | Kafka / Redpanda / NATS JetStream |
| **System of record / read model** | Immutable provenance + materialized COP | **Postgres — stays forever** | Postgres (unchanged) |
| **Fan-out** | Notify SSE gateways of COP change | Postgres `LISTEN/NOTIFY` | Redis / NATS / compacted topic |

Introducing a real log later replaces **only the transport layer**. Postgres
remains the queryable system of record. This is the mature CQRS topology; a clean
seam means we *slide into it*, not rewrite.

### 2.2 Persistence decision: PostgreSQL, single operational dependency

- Insights and all records live today in **one SQLite database**
  ([internal/store/store.go](../internal/store/store.go)), single connection,
  ephemeral on Cloud Run `/tmp`. That is the toy smell.
- Persistence is **already behind contract interfaces**
  (`ImmutableRecordRepository`, `AdvisoryHistoryReader`, `TransactionRunner`,
  `CheckpointRepository`). App code depends on the contracts, not SQLite — so a
  Postgres backend is a real second implementation, not a rewrite.
- **Postgres does the whole operational layer** — no Redis, no Kafka, no Mongo at
  current scale:
  - immutable append-only event log with **foreign keys enforcing provenance**;
  - ordered projection queue via `SELECT … FOR UPDATE SKIP LOCKED`;
  - fan-out via `LISTEN/NOTIFY` (replaces the in-process broker, gains
    cross-instance fan-out);
  - materialized COP read-model table for cheap `GET /cop`.

**Mongo was considered and rejected** for this system: the projector is
deterministic and order-sensitive (relational sequence + ACID gives provably
correct ordering; a sharded document store does not), and provenance integrity
wants real foreign keys and transactions (first-class in Postgres, app-enforced
in Mongo). The partition key we care about is a *logical per-incident sequence*,
not physical sharding — Postgres serves it with stronger guarantees.

### 2.3 Pluggability: the seams that make Kafka/Redpanda a drop-in

Three interfaces, shaped around **log semantics**, never SQL:

```go
// Append side. Backends: Postgres INSERT, or Kafka/Redpanda produce.
type EventLog interface {
    Append(ctx context.Context, e EventEnvelope) error
}

type EventEnvelope struct {
    PartitionKey   string // e.g. incident id — routing + per-key ordering
    IdempotencyKey string // source dedup; makes at-least-once safe
    Type           string
    Payload        []byte
}

// Read side. Ordered *per partition key*, at-least-once. The implementation
// owns position/offset tracking — callers never see it.
type EventConsumer interface {
    Run(ctx context.Context, handle func(context.Context, Event) error) error
    // handle returns nil => ack/advance; error => redeliver.
}

// Fan-out for the UI. Small payloads (a revision/id, not the whole COP).
type EventBus interface {
    Publish(ctx context.Context, topic string, note []byte) error
    Subscribe(ctx context.Context, topic string) (<-chan []byte, error)
}
```

**Postgres implementation now:** `Append` = INSERT with a unique constraint on
`IdempotencyKey`; `Run` = `SKIP LOCKED` claim grouped by `PartitionKey`,
processing each key in sequence order, checkpointing a cursor; `EventBus` =
`LISTEN/NOTIFY`.

**Kafka/Redpanda later:** `Append` = produce to a topic keyed by `PartitionKey`;
`Run` = consumer group with committed offsets; `EventBus` = compacted topic or
keep Redis/NATS. **Same interfaces, different wiring at composition. Producers and
consumers — including the simulation — do not change a line.**

### 2.4 The delivery contract (the rule that prevents coupling)

The interface promises the **weakest** semantics both backends honor, and no
consumer may rely on more:

- **At-least-once**, never exactly-once.
- **Ordered per partition key**, never globally ordered.

Our existing **P05 source idempotency** turns at-least-once into
effectively-once. The scale decision (per-incident partition key) and the
portability decision are the **same decision**.

### 2.5 The one real cost of portability (pay it now)

Postgres-only tempts you to append an event *and* project it in one ACID
transaction — Kafka cannot. So:

> The projector **consumes from the log interface** and commits
> `(projection update + position advance)` atomically — **never**
> `(append + project)`.

Both backends honor project-plus-position; only Postgres honors
append-plus-project. Choosing the atomic boundary both systems can honor is the
entire price of the Kafka door staying open. Everything else is free once the
interfaces are log-shaped.

### 2.6 Partition key = scale and determinism, one decision

- `partition_key` on events (default: incident id; domain-scoped for
  multi-dimension).
- A monotonic sequence gives total order; a *subsequence per key* is still
  ordered, so per-incident order is free.
- Projector workers claim `WHERE partition_key = …` via `SKIP LOCKED` and process
  each key in sequence order → **different incidents project in parallel, each
  strictly ordered**. This is the "1000 events at once" answer without breaking
  determinism.
- Physical parallelism later: declarative `PARTITION BY HASH (partition_key)` —
  same engine, no model change.

### 2.7 "New dimension" story

A new dimension (another feed / domain) becomes **a new producer + a consumer
group** on the same backbone — not a re-plumb. SQLite would starve on concurrent
writers and the in-process broker would be reimplemented; the log topology
removes both problems.

---

## 3. Simulation isolation and the three modes

### 3.1 Isolation

- Simulation becomes **its own package/module**. Dependency direction is the
  invariant: framework packages (ingestion, projector, store, terra, sol) are
  synchronous and timing-blind and **never import simulation**; simulation
  imports *them* and orchestrates over time.
- **All pacing/timing lives only in simulation.** The framework has no notion of
  "beat" or "delay."

### 3.2 Simulation drives the real pipeline (the honest reveal)

- Startup no longer bulk-seeds the interactive view. The board starts **empty**
  ("press Play scenario").
- A **`BeatExecutor`** (in the simulation package) runs each beat: `Append` the
  beat's frozen raw event to the `EventLog` → the projector advances a real
  revision → publish a COP snapshot → the UI reveals **progressively, for real**.
- At the beats that reach the advisory revisions, invoke the **real Terra/Sol
  services** (live client or recorded/fixture — see modes). The existing staged
  advisory logic in
  [internal/reference/domesticdisturbance/simulator/fixture_advisory.go](../internal/reference/domesticdisturbance/simulator/fixture_advisory.go)
  (`runTerraActive` rev7, `runSolRecommendation` rev7, `runTerraObsolete` rev9)
  is reused; it already persists via the Terra/Sol services and accepts injectable
  clients.

### 3.3 Three modes (cassette pattern)

A thin **decorator around the Terra/Sol `StructuredClient`**, entirely inside the
simulation package — the services can't tell the difference:

| Mode | Inference | API cost | Use |
|------|-----------|----------|-----|
| **Live** | Real GPT-5.6 via OpenAI transport; **records the run** | Yes | Bank one good run |
| **Replay (recorded)** | Replays the last live run's real outputs from the recording | **None** | The new **Replay** button; every video take |
| **Fixture** | Frozen checked-in advisories | **None** | CI, deterministic safe default |

Workflow win: do **one** paid live run to capture a recording, then hit **Replay
last run** for every take — real GPT-5.6 output, free and reliable on retries.
Enterprise angle: deterministic replay of real agent decisions is a genuine
audit/compliance capability, not demo theater.

### 3.4 Session isolation (durable epochs)

- The store is append-only + idempotent, so replay needs isolation.
- **A simulation session is a durable, first-class epoch:** every canonical
  event, insight, and model run carries a `session_id`; recovery/COP/advisories
  scope to the active session.
- Gives both properties at once: replay a run cleanly on camera **and** keep every
  past run durably auditable. With Postgres this is a `WHERE session_id = :active`
  scope, not object-swapping.
- **Active-session indirection:** the API read ports resolve "which session to
  show now" from the active session the simulation sets on Start/Reset. No active
  session → empty board.

### 3.5 Pacing and the flood

- Fix the **relative-to-start delay bug**: use **cumulative** delays.
- `MOSAIC_SIM_BEAT_SPACING` (default ~2.5s) controls spacing, in the simulation
  path only.
- The flood disappears by construction: each beat now does bounded real work (and
  at advisory beats awaits the model), so calls are naturally serialized and
  spaced.
- **Burst capability:** the simulation can be scaled to emit N events at once
  (e.g. 1000) to stress the per-key `SKIP LOCKED` projector. Reality doesn't need
  it; the system should survive it if someone asks.

### 3.6 Simulation beats (captured for reference)

Fixture: `datasets/domestic-disturbance/scenario.json`. Ten beats; end state COP
revision 9 (beat 8 quarantines and does not project).

| Order | Beat id | Current `delay_ms` | Board effect | Advisory |
|------:|---------|-------------------:|--------------|----------|
| 1 | `baseline-01-911-call` | 0 | Incident at 14 Cedar Lane | — |
| 2 | `baseline-02-welfare-check` | 100 | Location history / prior note | — |
| 3 | `baseline-03-weather-alert` | 100 | Heavy rain · Cedar district | — |
| 4 | `baseline-04-road-closure` | 100 | Main Street bridge blocked | — |
| 5 | `baseline-05-ems-availability` | 100 | EMS-4 available | — |
| 6 | `baseline-06-officer-update` | 100 | Unit 17 assigned / near address | — |
| 7 | `fixture-07-repaired-incomplete-road` | 100 | Brook Lane blocked; Luna repaired missing id (~rev 7) | **Terra access insight + Sol recommendation** |
| 8 | `fixture-08-quarantined-input` | 100 | Malformed payload quarantined — COP not mutated | — |
| 9 | `fixture-09-late-delivery` | 100 | EMS-4 unavailable (late update) | — |
| 10 | `fixture-10-road-correction` | 100 | Brook Lane open (debris cleared, ~rev 9) | **Terra obsoletes access insight** |

**Change:** delays become cumulative and spaced (~2.5s), driven by
`MOSAIC_SIM_BEAT_SPACING`; each beat is a real `EventLog.Append`, not an SSE
metadata blip. The frozen dataset is **not** edited — timing is presentation and
lives in the simulation path.

### 3.7 Agent prompts, structured output, and prompt provenance (Live mode)

Fixture/Replay modes do not call the model, so this is a **Live-mode** concern —
but Live (and the recorded run you bank from it) is only as good as this, and
today it is the weakest link. Current state:

- **Two divergent prompt sources.** Curated
  [prompts/terra/v1.0.0.md](../prompts/terra/v1.0.0.md) and
  [prompts/sol/v1.0.0.md](../prompts/sol/v1.0.0.md) are detailed and disciplined
  but **orphaned** — the live path uses thin inline Go constants
  (`terraInstructions` / `solInstructions` / `lunaInstructions` in
  [internal/openaimodel](../internal/openaimodel/terra.go)), not the files.
- **Luna has no curated prompt and the thinnest inline one** — yet it is the most
  demanding agent (entity extraction, schema-valid canonical events,
  accept/repair/quarantine decisions). The quarantine → repair → late-delivery
  narrative depends on Luna behaving.
- **No real schema enforcement on the wire.** The OpenAI structured-output format
  is a stub (`type: object, additionalProperties: true, strict: false` in
  [transport.go](../internal/openaimodel/transport.go)) — it does **not** send
  `insight/recommendation/luna_result.schema.json`. Output correctness rests
  entirely on the prompt; the service validators then reject malformed output as
  `invalid`. Expect a high invalid/refused rate in Live mode today.
- **Prompt provenance is broken.** `ModelRun.PromptVersion` records
  `"mosaicdemo-interactive-v1"`, which corresponds to no retrievable artifact
  (files are `v1.0.0`; inline constants are unversioned). A provenance-first
  system currently cannot answer "which exact prompt produced this Insight?"

**Target design:** one versioned source of truth loaded from `assetRoot` at
composition (the code already reserves this —
`_ = assetRoot // reserved for future profile-relative prompt assets` in
[cmd/mosaicdemo/models.go](../cmd/mosaicdemo/models.go)); the real JSON schema sent
as the **strict** structured-output format; and `ModelRun.PromptVersion` recorded
as **file version + content hash**, so every live decision traces to an exact,
retrievable prompt. The cassette captures prompt version + hash so replayed runs
keep honest provenance. **Fixture mode stays prompt-independent — the
guaranteed-safe demo path.**

---

## 4. UI changes (minimal)

- Empty initial board state ("press Play scenario") — already supported; verify.
- **Progressive reveal is free** once per-beat ingestion is real (the UI already
  reloads COP/advisories on beat events and consumes `cop.snapshot`).
- **New "Replay last run" button** (mode 2) plus mode/status surfacing (Live /
  Replay / Fixture).
- "Refresh advice" decision: keep as a manual re-poll, or promote to a manual
  Terra trigger (TBD during D2).

---

## 5. Honesty guarantee — what stays untouched

The following framework code is **not** altered; the reveal is real, not faked:

- ingestion pipeline logic and Luna normalization;
- deterministic projector and recovery algorithm;
- Terra/Sol service logic;
- ontology / JSON schemas;
- the frozen `domestic-disturbance` dataset (integrity, sha256, id_map).

Proof: every existing deterministic-core test stays green unchanged.

---

## 6. Deployment / packaging

**Decision: two containers.** The **stateless app** and **Postgres** run as
separate services so app instances scale horizontally against shared state. This
is the target topology, and the only one v0.4 builds. Postgres-only means exactly
**one external dependency** (Redis removed from the plan), but that is one
*dependency*, not one *container* — the app and the database stay distinct.

- **Two-container (decided):** app (N replicas) + Postgres as its own stateful
  service → K8s-native. K8s manifests are the last mile; the stateless +
  externalized-state + pub-sub-interface work is what earns them.
- **Single-container appliance (optional, later — not chosen):** app + Postgres in
  one image (supervisor + entrypoint) is a legitimate *packaging* choice for
  turnkey single-node installs (cf. GitLab Omnibus). It is **not** the scalable
  topology and is **out of scope for v0.4** unless we explicitly add it.

---

## 7. Task breakdown (T-shirt sizing)

Sizes: **S** (≤ half day) · **M** (~1–2 days) · **L** (~3–5 days) · **XL** (> week).
Dependencies noted. Workstreams A→B are the foundation; C rides on them.

### Working agreement (multi-agent)

- All v0.4 work happens on branch **`feat/v0.4-pluggable-event-spine`**.
- **Claim** a parcel by putting your agent name/id in the **Claim** column and
  setting **Status** to `In progress`; move to `In review`/`Done` when finished.
  `Todo` means unclaimed and available.
- **Commit only the changes you made** — scope every commit to your parcel's own
  files, do not sweep unrelated edits, and prefix the commit subject with the
  parcel id (e.g. `A1: define event-log interfaces`). One agent's commit must not
  contain another parcel's work.
- Do **not** start a parcel whose **Deps** are not yet `Done`.
- **Status legend:** `Todo` · `In progress` · `In review` · `Done` · `Blocked`.

### Workstream A — Event spine (foundation)
| ID | Task | Size | Deps | Claim | Status |
|----|------|------|------|-------|--------|
| A1 | Define `EventLog` / `EventConsumer` / `EventBus`, envelope, position; document the delivery contract | **M** | — | Opus agent | Done (`internal/eventlog`, 8a4ca53) |
| A2 | Partition-key model: `partition_key` column, monotonic sequence, consumer checkpoint/cursor table | **M** | A1 | a2-partition-model | Done (`internal/pgstore` 0002 + tokens) |

### Workstream B — Postgres backbone
| ID | Task | Size | Deps | Claim | Status |
|----|------|------|------|-------|--------|
| B1 | `pgstore` implementing existing repository contracts; port schema + migrations; Postgres tx semantics (drop single-conn assumptions) | **L** | — | Opus agent | Done (`internal/pgstore`, 1f4937f) |
| B2 | `EventLog.Append` on Postgres (INSERT + idempotency unique constraint) | **M** | A1, B1 | b2-pg-eventlog-append | Done (`pgstore.Store.Append`) |
| B3 | `EventConsumer` via `SKIP LOCKED`, per-partition ordered, checkpointed; atomic project+position; multi-worker | **L** | A2, B2 | b3-pg-event-consumer | Done (`pgstore.EventConsumer`, advisory locks) |
| B4 | `EventBus` via `LISTEN/NOTIFY`; replace in-process broker behind the interface | **M** | A1, B1 | b4-pg-event-bus | Done (`pgstore.EventBus` LISTEN/NOTIFY) |
| B5 | Materialized COP read-model table maintained by projector; `GET /cop` reads it | **M** | B3 | — | Todo |

### Workstream C — Simulation isolation & modes
| ID | Task | Size | Deps | Claim | Status |
|----|------|------|------|-------|--------|
| C1 | Extract simulation into its own package/module; enforce dependency direction | **M** | — | c1-sim-extract | Done |
| C2 | `BeatExecutor` — per-beat real `Append`; cumulative pacing + `MOSAIC_SIM_BEAT_SPACING`; optional burst mode | **M** | B2, C1 | — | Todo |
| C3 | Session isolation — `session_id` epoch; scoped recovery/COP/advisories; active-session indirection in API | **L** | B1, B5 | — | Todo |
| C4 | Cassette — record/replay decorator around Terra/Sol `StructuredClient`; recording persistence keyed by beat/revision | **L** | C1 | — | Todo |
| C5 | Three-mode wiring (Live / Replay / Fixture) + config + provider selection | **M** | C4 | — | Todo |

### Workstream D — UI
| ID | Task | Size | Deps | Claim | Status |
|----|------|------|------|-------|--------|
| D1 | Empty initial board + progressive-reveal verification | **S** | C2, C3 | — | Todo |
| D2 | "Replay last run" button + mode/status surfacing | **M** | C5 | — | Todo |

### Workstream E — Ops & pluggability proof
| ID | Task | Size | Deps | Claim | Status |
|----|------|------|------|-------|--------|
| E1 | `docker-compose` = **two services** (stateless app + Postgres), the decided topology; app stays stateless. Single-container appliance is out of scope for v0.4 | **M** | B1 | Opus agent | Done (fae95de, df9177b) |
| E2 | Interface **conformance test suite** (validates Postgres now; same suite validates a future Kafka/Redpanda impl) | **M** | A1 | — | Todo |
| E3 | Kafka/Redpanda introduction guide (implement the seams; wiring swap; Postgres stays read model) | **S** | A1 | — | Todo |

### Workstream F — Tests & docs
| ID | Task | Size | Deps | Claim | Status |
|----|------|------|------|-------|--------|
| F1 | Simulation-driven progressive projection; session replay isolation; live/recorded/fixture parity; framework-untouched proof | **L** | C3, C5 | — | Todo |
| F2 | Update `demo-script.md` / `demo-video.md` for the now-real reveal | **S** | D1 | — | Todo |

### Workstream G — Capture (original goal)
| ID | Task | Size | Deps | Claim | Status |
|----|------|------|------|-------|--------|
| G1 | Playwright capture keyed off real intermediate rail states + synthetic cursor + paced holds | **M** | C2, C3, D2 | — | Todo |

### Workstream H — Agent prompts & structured output (Live-mode quality)
| ID | Task | Size | Deps | Claim | Status |
|----|------|------|------|-------|--------|
| H1 | Single prompt source of truth: load versioned prompt files from `assetRoot` at composition; remove inline-constant divergence; record honest `PromptVersion` = file version + content hash in `ModelRun` | **M** | — | h1 | Done |
| H2 | Author a proper **Luna** prompt (new artifact) grounded in the ontology: entity kinds, canonical event types, ID conventions, repair-vs-quarantine policy, evidence citation, injection resistance | **L** | H1 | h2 agent | In progress |
| H3 | Reconcile + strengthen **Terra** and **Sol** prompts: make the curated `.md` the loaded source; enrich with domain vocabulary + schema-field expectations; keep existing claim/lifecycle/safety discipline | **M** | H1 | h3 agent | In progress |
| H4 | Send the **real JSON schema** as the OpenAI structured-output format (`strict: true`) for insight/recommendation/luna_result — API-side shape enforcement so the prompt carries semantics, not structure | **M** | — | h4 agent | In progress |
| H5 | Prompt **eval harness**: run each prompt against fixture inputs; assert schema-valid + expected semantics; regression guard against prompt drift | **M** | H2, H3, H4 | — | Todo |
| H6 | Cassette records prompt version + content hash so replayed runs keep honest provenance | **S** | C4, H1 | — | Todo |

**Critical path:** A1 → A2 → B2 → B3 → B5 → C3 → D1 → G1. Cassette (C4/C5) runs in
parallel at the client layer. E2 gates the "pluggable" claim and should land with
A1. **Workstream H gates Live-mode quality** — H1/H4 are prerequisites for a
usable "bank one live run" workflow; Fixture mode is unaffected and remains the
safe path if H slips.

---

## 8. Fallback (if the spine does not land)

`main` must always retain a working demo. If the re-architecture cannot be
completed to satisfaction, retreat to **cosmetic UI/simulation-only** changes on
top of the current seeded model — no framework or persistence risk:

| ID | Task | Size | Claim | Status |
|----|------|------|-------|--------|
| FB1 | Beat pacing only: cumulative delays / `MOSAIC_SIM_BEAT_SPACING` on the existing seeded model (kills the flood, paces the beat list/clock) | **S** | — | Todo |
| FB2 | "Replay last run" as a re-poll of the existing seeded advisories (cosmetic) | **S–M** | — | Todo |
| FB3 | Playwright capture against the existing seeded board | **M** | — | Todo |

The board still jumps blank→final in fallback; the beat story is carried by VO and
B-roll (see [demo-video.md](demo-video.md)). This is the safety net, not the goal.

---

## 9. Verification gates

- Existing gates stay green: `go test ./tests/e2e -count=1`,
  `go run ./cmd/mosaic quality`, `npm run check`, `npm run build`, Docker Compose
  smoke.
- New: interface conformance suite (E2); progressive-projection + session-replay +
  mode-parity tests (F1); Postgres migration/round-trip tests (B1).
- Honesty proof: the untouched-framework test set (Section 5) passes with no edits.

---

## 10. Relationship to other docs

| Doc | Role |
|-----|------|
| [HANDOFF.md](../HANDOFF.md) | Live coordinator board (v0.3 verified) |
| **This file** | v0.4 design + task parcels for the pluggable event spine |
| [demo-script.md](demo-script.md) | Presenter pitch and UI actions |
| [demo-video.md](demo-video.md) | YouTube cut; beat→visual map (becomes real under v0.4) |
| [runbook/cloud-run-deployment-analysis.md](runbook/cloud-run-deployment-analysis.md) | Durable deployment analysis |
