# Mosaic v0.2 Fixture Advisory Composition Handoff

This is the live coordination surface for the fixture-advisory increment. The
completed v0.1 foundation board is preserved at
[`docs/archive/HANDOFF-v0.1-foundation.md`](docs/archive/HANDOFF-v0.1-foundation.md).

## Increment goal

Expose the already-validated synthetic Terra/Sol advisory history in the local
demo without introducing a live model transport, a raw-payload read surface,
or an operational action. The historical rev-7 advisory is explicitly made
non-current by the frozen rev-9 road correction; the UI must make that state
unambiguous.

## Mandatory read order

Before claiming or changing a parcel, read in full:

1. [`AGENTS.md`](AGENTS.md)
2. [`docs/rfcs/RFC-0001-mosaic-demo-foundation.md`](docs/rfcs/RFC-0001-mosaic-demo-foundation.md)
3. [`docs/rfcs/RFC-0002-public-pluggability-and-agent-observability.md`](docs/rfcs/RFC-0002-public-pluggability-and-agent-observability.md)
4. [`docs/rfcs/RFC-0003-fixture-advisory-composition.md`](docs/rfcs/RFC-0003-fixture-advisory-composition.md)
5. This file in full.
6. Your parcel, every prerequisite parcel, and the exact tests/contracts it names.

The coordinator alone changes this board, `docs/rfcs/**`, or a parcel's status.
External builders may work from this file, but must not amend it in their
branch. Report the branch, base SHA, commit SHA, and verbatim validation output
to the coordinator for integration.

## Starting point and branch model

The integration branch is `mosaic/v0.1-foundation`. The increment began at
`c03ba39` after P01–P20; P21–P28 are integrated through `8d15b0b`.
Every new claim bases from the latest integrated branch SHA recorded by the
coordinator, never from an older parcel worktree.

Every implementation parcel uses a new isolated branch and worktree from the
latest integrated `mosaic/v0.1-foundation` SHA:

```text
mosaic/v0.1-foundation
├─ parcel/P21-fixture-advisory-rfc                 (integrated)
├─ parcel/P22-advisory-history-contracts           (integrated)
├─ parcel/P23-advisory-history-store                (integrated)
├─ parcel/P24-fixture-advisory-replay               (integrated)
├─ parcel/P25-public-advisory-api                   (integrated)
├─ parcel/P26-advisory-dashboard                    (integrated)
├─ parcel/P27-advisory-composition                  (integrated)
├─ parcel/P28-advisory-acceptance                   (integrated)
└─ parcel/P29-local-feed-generation                 (integrated; candidate rejected, no freeze)
```

Do not reuse a prior P01–P20 worktree. Do not claim a row until all of its
prerequisites are marked `✅ Integrated` on this board.

## Parcel board

| ID | Work | Prereqs | Owns (exclusive) | Status |
|---|---|---|---|---|
| P21 | Re-baseline the handoff and record RFC-0003's fixture-only advisory decision | P20 | `HANDOFF.md`, `docs/archive/HANDOFF-v0.1-foundation.md`, `docs/rfcs/RFC-0003-fixture-advisory-composition.md` | ✅ Integrated — `48d96c8` |
| P22 | Add the additive, read-only advisory-history contract and regenerate checked-in GoMock output | P21 | `internal/contracts/**` | ✅ Integrated — `17a4cde` |
| P23 | Implement deterministic SQLite reads for the P22 advisory history contract; no migration | P22 | `internal/store/**` | ✅ Integrated — `8cbc905` |
| P24 | Deterministic fixture Terra/Sol replay with immutable lifecycle/audit history | P22, P23 | `internal/simulator/**` | ✅ Integrated — `e37c17a` |
| P25 | Bounded public advisory API and truthful advisory capability status | P22, P23 | `internal/api/**` | ✅ Integrated — `8bde753` |
| P26 | Advisory-history dashboard cards, evidence links, and supersession presentation | P25 | `ui/**` | ✅ Integrated — `24c7d70` |
| P27 | Local executable composition of fixture replay, advisory history, and public API | P24, P25 | `cmd/mosaicdemo/**` | ✅ Integrated — `3ecbefb` |
| P28 | Public advisory API/UI/Docker/runbook acceptance proof | P26, P27 | `tests/e2e/**`, `docs/runbook/**` | ✅ Integrated — `8d15b0b` |
| P29 | Generate and inspect one rate-bounded Cerebras `gemma-4-31b` synthetic feed candidate for controlled demo playback; do not freeze it | P28 | `internal/datasetgen/**`, `cmd/datasetgen/**`, `localmodels/staging/domestic-disturbance-v2/**`, `docs/dataset-generation.md` | ✅ Integrated — `03d68a7`; candidate rejected by read-only schema validation and not frozen |
| P30 | Create a new versioned synthetic-only prompt and, only after explicit approval, generate and inspect one fresh Cerebras `gemma-4-31b` candidate; do not freeze it | P29 | `prompts/datasetgen/**`, `localmodels/staging/domestic-disturbance-v3/**`, `docs/dataset-generation.md` | ⛔ Blocked — awaits user authorization for one additional provider request |

## P22 builder brief — advisory-history contract

### Goal

Add the smallest stable cross-package read seam needed by later fixture
composition and public read-model parcels. This is a read contract only; it
must not create an operational command or alter append-only storage semantics.

### Required shape

Add `AdvisoryHistoryReader` and an `AdvisoryHistory` value in
`internal/contracts/`. The method takes a context and returns a complete
snapshot of persisted advisory records:

- `Insights []gen.Insight`
- `Recommendations []gen.Recommendation`
- `ModelRuns []gen.ModelRun`
- `AuditRecords []gen.AuditRecord`

`ModelRuns` is restricted by implementations to Terra/Sol records. The
contract is intentionally a domain read seam, not an HTTP response and not a
generic database export. It carries no raw event, canonical-event, payload,
checksum, prompt, response, credential, or operational-action data.

Keep the change additive. Update contract tests and regenerate
`internal/contracts/mocks/contracts_mock.go` with the pinned GoMock tool. Do
not change authored ontology schemas, generated ontology code, package module
dependencies, or any path outside P22 ownership.

### Acceptance

- `go generate ./internal/contracts/mocks` changes only the checked-in mock as
  required by the source contract.
- Contract and generated mock tests compile and prove the new reader surface.
- `go vet ./internal/contracts/...` and `go test ./internal/contracts/...`
  pass.
- `task quality` passes from the parcel worktree.

## P23 builder brief — SQLite advisory-history reader

### Goal

Make `*store.Store` implement `contracts.AdvisoryHistoryReader` using
read-only, deterministic SQLite queries over existing append-only tables. It
is a query adapter, not a migration and not a write path.

### Required behavior

- Return an empty, usable history when there are no advisory records.
- Return decoded immutable records in stable chronological order, breaking
  equal timestamps by the record ID:
  - Insights: `created_at`, `insight_id`
  - Recommendations: `created_at`, `recommendation_id`
  - Terra/Sol Model Runs only: `completed_at`, `model_run_id`
  - Audit Records: `created_at`, `audit_record_id`
- Preserve the JSON-decoded generated types exactly; a malformed stored record
  or query failure is an error, never a silently skipped row.
- Use only `SELECT` queries. Do not alter migrations, tables, append methods,
  triggers, or immutability behavior.
- Assert compile-time satisfaction of the P22 interface.

### Acceptance

- Focused tests persist mixed records, prove filtering/order/empty history,
  and prove a malformed stored JSON record fails closed.
- `git diff --exit-code -- migrations` succeeds.
- `go vet ./internal/store/...`, `go test ./internal/store/...`, and
  `task quality` pass.

## P24 builder brief — fixture advisory replay

### Goal

Extend only `internal/simulator/` with an explicit fixture-advisory replay
entry point. It must use the existing P10 Terra and P11 Sol services with local
fixture clients, never a network or live model client. It consumes the loaded
P04 expected outcomes plus P22/P23 interfaces; do not change datasets,
ontology, P10/P11, contracts, store, migrations, or the executable root.

### Required behavior

- Replay the exact historical phases using the scenario's rev-7 and rev-9
  timeline snapshots, not the final COP:
  1. Terra active Insight at rev 7;
  2. fixture `briefing_requested` Audit Record;
  3. Sol Recommendation at rev 7 for `supervisor-demo`;
  4. Terra obsolete Insight at rev 9; and
  5. fixture recommendation acknowledgement Audit Record.
- Validate candidate outputs through P10/P11, cite only permitted persisted
  evidence, and create deterministic fixture Model Run identities/clocks.
- Commit each Model Run/output pair through the existing transaction seam.
  An intact restart appends no duplicate artifact. An absent stage may run; a
  partial stage is an integrity error and must stop without rewriting history.
- The fixture service is not a projector, cannot alter COP state, and cannot
  make an operational call. It uses no credentials, GGUF, shell, or network.

### Acceptance

- Focused simulator tests prove fresh replay, intact-restart idempotency,
  rev-7/rev-9 snapshot selection, evidence/lifecycle correctness, and partial
  stage failure.
- Tests prove a refusal/invalid fixture response records the Model Run but
  creates no Insight/Recommendation and changes no COP.
- `go vet ./internal/simulator/...`, `go test ./internal/simulator/...`, and
  `go run ./cmd/mosaic quality` pass.

## P25 builder brief — public advisory API

### Goal

Add only `internal/api/` support for a bounded public `GET /api/v1/advisories`
read model. Consume `contracts.AdvisoryHistoryReader` through `api.Config`; do
not query SQLite directly, compose a fixture, or change persistence.

### Required behavior

- Add a distinct policy action and route. The public default allows it; an
  injected deny policy must return the established denied response.
- Recover the COP, read the P22 history, and return only cited Insight and
  Recommendation artifacts plus minimal lifecycle/composition status. Never
  return raw payloads, checksums, prompts, model responses, secrets, or generic
  Audit Record/Model Run contents.
- Derive `historical`, `current`, `superseded`, and `not_current` strictly from
  the recovered revision, Insight lifecycle links, and cited evidence. The
  rev-7 fixture recommendation is not current at rev 9.
- Make operations capability status configuration-driven: fixture-composed only
  when composition explicitly supplies it; otherwise unavailable. Preserve all
  P17 public actor/policy behavior and existing route responses.

### Acceptance

- API tests prove public no-header reads, replacement-policy denial, bounded
  serialization, resolver/history failure handling, and rev-9 status results.
- Tests prove an empty/uncomposed history never claims live Terra/Sol transport.
- `go vet ./internal/api/...`, `go test ./internal/api/...`, and
  `go run ./cmd/mosaic quality` pass.

## P26 builder brief — advisory dashboard

### Goal

Update only `ui/` to render P25's bounded advisory response. Do not infer an
assessment from COP facts or hard-code fixture IDs as a substitute for the API.

### Required behavior

- Replace withheld advisory placeholders with evidence-resolvable historical
  Insight/Recommendation cards when the API supplies them.
- Label the rev-7 active Insight as superseded and its Recommendation as not
  current after the rev-9 correction. Show no current assessment or advice in
  that state.
- Keep explicit fixture-composed versus live-transport-unavailable language.
  Review affordances may prefill a supported immutable target, but every write
  remains the existing `executed: false` audit operation.
- Preserve raw-payload omission, existing COP/evidence behavior, and public
  no-header operation. No API, composition, or CSS outside `ui/**` is owned.

### Acceptance

- Svelte checks cover ready, empty, unavailable, superseded, and not-current
  display states without raw/model-response leakage.
- `npm run check`, `npm run build`, and `go run ./cmd/mosaic quality` pass.

## P27 builder brief — executable advisory composition

### Goal

Update only `cmd/mosaicdemo/` so local startup composes the frozen scenario,
P24 fixture-advisory replay, P23 history reader, and P25 public API before the
existing static dashboard is served.

### Required behavior

- Startup remains local, synthetic, single-instance, and fixture-only. It does
  not construct a live model client or operational-system client.
- A fresh database persists the deterministic scenario and one complete
  advisory history. Reopening a retained SQLite file verifies/reuses that exact
  history without duplicates; an incomplete sequence stops startup visibly.
- Inject the history reader and explicit fixture-composed capability state into
  P25. Retain the public actor/policy defaults and guarded static UI host.
- Do not alter UI source, API source, simulator source, Docker files, datasets,
  migrations, or module dependencies.

### Acceptance

- Package tests prove fresh startup exposes the public advisory endpoint and
  a retained-volume restart adds no advisory records.
- Tests prove uncomposed/live transport is never selected and partial history
  is surfaced as an error.
- `go vet ./cmd/mosaicdemo/...`, `go test ./cmd/mosaicdemo/...`, and
  `go run ./cmd/mosaic quality` pass.

## P28 builder brief — advisory acceptance and runbook

### Goal

Update only end-to-end proof and the local Docker runbook for the composed
fixture advisory story. The image and Compose files are exercised but not
modified unless the coordinator opens a dedicated Docker parcel.

### Required behavior and acceptance

- E2E/public proof uses the executable composition, no identity header, and
  verifies rev-9 COP, bounded `/api/v1/advisories` fields, superseded/not-current
  wording, evidence resolution, and immutable `executed: false` review writes.
- Restart proof verifies a retained SQLite volume does not duplicate fixture
  Insights, Recommendations, Model Runs, or fixture Audit Records.
- Runbook documents the fixture-only history, exact public checks, retained
  volume behavior, and continued absence of live model/reconciliation/external
  action claims.
- Run Svelte check/build, `go run ./cmd/mosaic quality`, and a fresh isolated
  Docker build/start/smoke. Report each command verbatim.


## P29 coordinator brief — rate-bounded synthetic feed candidate

### Goal

Generate one staged Cerebras `gemma-4-31b` candidate for a future controlled
playback demo. The model creates synthetic source-feed artifacts before the
demo; a later parcel may replay a reviewed, frozen version through the normal
ingestion path. P29 does not change the checked-in dataset, startup
composition, public API, UI, or any live-model policy.

### Required behavior

- Use Cerebras `gemma-4-31b` through an explicit runtime credential only. No
  credential, provider response, generated candidate, or real record may enter
  Git.
- Respect the provider budget: one tiny no-data readiness smoke and at most one
  fixed-seed candidate request, with no automatic retries. Stop on rate limit,
  timeout, refusal, or invalid output.
- Generate only into the initially empty
  `localmodels/staging/domestic-disturbance-v2/` directory, for scenario
  `domestic-disturbance` and a recorded fixed seed.
- Before generation, run the package-generation test and validate the existing
  frozen fixture. After generation, inspect the staged provenance, manifest,
  scenario, and a bounded sample of raw events for synthetic-only content,
  expected temporal ordering, corrections, and internally consistent IDs.
- Do not run `datasetgen freeze` until the coordinator and user have reviewed
  the staged candidate. Promotion is a separate, explicitly approved parcel.
- Update the generation runbook with the provider selection, runtime-only
  credential requirement, budget, and staging-only workflow.

### Acceptance

- `go test ./internal/datasetgen/... -count=1` and
  `go run ./cmd/datasetgen validate` pass before generation.
- The readiness smoke sends no repository or operational data. A successful
  single candidate request produces exactly the documented stage layout and
  valid provenance without writing under `datasets/`.
- The coordinator records the requested model, seed, staged response checksum,
  and concise spot-check outcome in the P29 handoff note; no staged content is
  committed.

## P30 coordinator brief — corrected synthetic feed candidate

### Goal

Produce one fresh, staged, synthetic-only candidate after P29's recorded
schema-validation failure. P29's rejected candidate is immutable evidence and
must not be edited, repaired, or frozen.

### Required behavior

- Keep `prompts/datasetgen/v1.md` unchanged. Add a new versioned prompt that
  explicitly requires every supplied schema version to appear in the manifest
  and directs the model to self-check the complete bundle before returning it.
- Use a new ignored stage directory
  `localmodels/staging/domestic-disturbance-v3/`; never overwrite P29's stage.
- Use only Cerebras `gemma-4-31b`, one fixed seed, one candidate request, and
  no automatic retries. A coordinator must obtain explicit user authorization
  before making that new provider request.
- Run `datasetgen validate-stage` after generation. On any failure, preserve
  the staged response, record the exact error, and make no repair or freeze.

### Acceptance

- The new prompt has a semantic version distinct from P29's v1 prompt and its
  checksum is recorded in staged provenance.
- The one approved request yields a candidate that passes `validate-stage`, is
  manually spot-checked for synthetic-only content and temporal consistency,
  and remains outside `datasets/`.
- The handoff note records model, seed, output checksum, validation result, and
  the decision not to freeze pending separate approval.
## Shared-file mutexes

| Path | Owner / rule |
|---|---|
| `AGENTS.md`, `HANDOFF.md`, `docs/rfcs/**`, `docs/archive/**` | Coordinator only; external builders do not edit coordination documents |
| `internal/contracts/**` | P22 integrated; frozen unless a new dedicated contract parcel is approved |
| `internal/store/**` | P23 integrated; frozen unless a new dedicated store parcel is approved |
| `internal/simulator/**` | P24 integrated; frozen unless the coordinator opens a dedicated parcel |
| `internal/api/**` | P25 integrated; frozen unless the coordinator opens a dedicated parcel |
| `ui/**` | P26 integrated; frozen unless the coordinator opens a dedicated parcel |
| `cmd/mosaicdemo/**` | P27 integrated; frozen unless the coordinator opens a dedicated parcel |
| `tests/e2e/**`, `docs/runbook/**` | P28 integrated; frozen unless the coordinator opens a dedicated parcel |
| `internal/datasetgen/**`, `cmd/datasetgen/**` | P29 integrated; frozen unless the coordinator opens a dedicated generator parcel |
| `localmodels/staging/domestic-disturbance-v2/**` | P29 rejected candidate; immutable ignored evidence that must not be edited, frozen, or committed |
| `prompts/datasetgen/**`, `localmodels/staging/domestic-disturbance-v3/**`, `docs/dataset-generation.md` | Reserved for P30 only after it is explicitly authorized and claimed |
| `ontology/**`, `internal/ontology/**`, `migrations/**`, `go.mod`, `go.sum`, `Taskfile.yml`, `Dockerfile`, `docker-compose.yml` | Frozen for P24–P28 unless the coordinator opens a dedicated parcel |

## Integration and external handoff template

An external builder's handoff must contain exactly this information, followed
by the raw command output:

```text
Parcel: P##
Base integration SHA: <latest mosaic/v0.1-foundation SHA>
Branch / worktree: parcel/P##-... / .worktrees/P##-...
Owned paths changed: <only the paths listed on this board>
Commit SHA: <one focused commit>
Validation commands and results:
<verbatim output>
Unrelated changes: none
```

The coordinator verifies the diff is within the parcel's owned paths, merges
the parcel, reruns the complete quality gate, then records `✅ Integrated`.

## Execution waves and claim rules

```text
Wave A:  P24 fixture replay ∥ P25 public advisory API — completed
Wave B:  P26 dashboard (after P25) ∥ P27 executable composition (after P24/P25) — completed
Wave C:  P28 end-to-end/Docker/runbook proof (after P26/P27) — completed
Wave D:  P29 Cerebras synthetic feed candidate (after P28) — completed; candidate rejected, no freeze
Wave E:  P30 corrected prompt and fresh candidate (after P29) — blocked pending explicit user request authorization
```

P29's rejected stage is immutable ignored evidence. P30 must not be claimed or
send a provider request until the coordinator records a new user authorization.
## Notes

Format: `YYYY-MM-DD P## <claimed|ready|integrated|blocked> by <owner> — note`.

- 2026-07-19 P21 claimed by coordinator — base `c03ba39`, branch `parcel/P21-fixture-advisory-rfc`, worktree `.worktrees/P21-fixture-advisory-rfc`; archive completed v0.1 handoff and define the next external-builder-ready parcels.
- 2026-07-19 P21 integrated by coordinator — `48d96c8`; archived the completed v0.1 board, created RFC-0003, and released P22 for an isolated external-builder claim.
- 2026-07-19 P22 claimed by coordinator — base `4b9a69e`, branch `parcel/P22-advisory-history-contracts`, worktree `.worktrees/P22-advisory-history-contracts`; additive advisory-history contract and regenerated GoMock output only.
- 2026-07-19 P22 integrated by coordinator — `17a4cde`; reviewed the additive contract/mock change and reran `go run ./cmd/mosaic quality` successfully.
- 2026-07-19 P23 claimed by coordinator — base `bec2744`, branch `parcel/P23-advisory-history-store`, worktree `.worktrees/P23-advisory-history-store`; deterministic SQLite advisory-history reads only, with no migrations.
- 2026-07-19 P23 integrated by coordinator — `8cbc905`; bounded read-only SQLite history now filters Terra/Sol Model Runs, orders real RFC-3339 instants deterministically, and fails closed for selected corrupt records; full quality passed.
- 2026-07-19 P24–P28 planned by coordinator — RFC-0003 and this board now contain exclusive ownership, dependencies, acceptance proof, waves, and external-builder handoff instructions; P24/P25 are ready but unclaimed.
- 2026-07-20 P25 claimed by external builder — base `5bbf27a`, branch `parcel/P25-public-advisory-api`; submitted from the root worktree contrary to the isolated-worktree rule. The coordinator preserved that clean branch and used a separate integration worktree.
- 2026-07-20 P25 integrated by coordinator — `8bde753`; public bounded advisory read route, policy seam, and configuration-driven capability status passed review and the full quality gate. P26 is now ready.
- 2026-07-20 P24 integrated by coordinator — `e37c17a`; deterministic rev-7/rev-9 fixture replay now commits successful Terra/Sol Model Run-output pairs atomically, records failure Model Runs, detects partial history, and passed the full quality gate. P27 is now ready.
- 2026-07-20 P27 claimed by coordinator — base `613929f`, branch `parcel/P27-advisory-composition`, worktree `.worktrees/P27-advisory-composition`; compose local fixture replay, history reader, and public advisory API only.
- 2026-07-20 P27 integrated by coordinator — `3ecbefb`; local startup now composes P24 before the bounded API, exposes fixture-composed advisory history, avoids retained-volume duplicates, and visibly fails on partial history. Full quality passed.
- 2026-07-20 P26 claimed by coordinator — completion review of submitted `e08cdd7` in `.worktrees/P26-advisory-dashboard`; add the missing explicit empty/unavailable advisory states before integration.
- 2026-07-20 P26 integrated by coordinator — `24c7d70`; bounded advisory cards now cover loading, unavailable, empty, superseded, and not-current states with evidence resolution and immutable review prefill. Svelte check/build and full quality passed.
- 2026-07-20 P28 claimed by coordinator — base `24c7d70`, branch `parcel/P28-advisory-acceptance`, worktree `.worktrees/P28-advisory-acceptance`; complete public API/UI/restart/runbook/Docker acceptance proof only.
- 2026-07-20 P28 integrated by coordinator — `8d15b0b`; real executable no-header E2E/restart proof, Svelte check/build, full quality, and a fresh isolated `mosaic-p28smoke` Docker build/public smoke/restart all passed. The disposable Compose volume was removed after the check.
- 2026-07-20 P29 claimed by coordinator — base `b050618`, branch `parcel/P29-local-feed-generation`, worktree `.worktrees/P29-local-feed-generation`; generate one ignored local-model candidate and inspect it before any promotion.
- 2026-07-20 P29 integrated by coordinator — `03d68a7`; added a one-shot, no-retry Cerebras `gemma-4-31b` generator, credential-safe remote provenance, and read-only `datasetgen validate-stage`, with focused tests and full quality passing. The no-data readiness smoke returned `READY`; the one candidate used seed `20260720` and staged response SHA-256 `56dfc808cbb94b99fb52b9d18f5230efa368dcb7f06550adc5d17302574f5dfe`. Provenance and layout spot checks passed (no credential/local identity; retry disabled), but `validate-stage` rejected the manifest because `schema_versions.audit-record.schema.json` was absent. The ignored stage remains unchanged; no repair, retry, freeze, or commit of generated content occurred. P30 is blocked pending a new explicit user authorization.
