# Mosaic v0.1 Foundation Handoff

This is the single live coordination surface for the first Mosaic build cycle.
It links to the design; it does not duplicate it. Completed cycles move to
`docs/archive/` and a new handoff starts fresh.

## Read order

1. [`AGENTS.md`](AGENTS.md)
2. [`docs/rfcs/RFC-0001-mosaic-demo-foundation.md`](docs/rfcs/RFC-0001-mosaic-demo-foundation.md)
3. This file
4. Your parcel and its prerequisites
5. The contracts, schemas, and package tests your parcel consumes

## Current state

The repository is a greenfield control-plane baseline. The product vision and
technical specification is in
[`Mosaic_Architecture_and_Technical_Specification.md`](Mosaic_Architecture_and_Technical_Specification.md).

The v0.1 target is one local, synthetic, replayable domestic-disturbance demo:
Raw Event → Canonical Event → deterministic COP → evidence-backed Insight →
supervisor-reviewed Recommendation → Audit Record. No real public-safety data,
hosted deployment, or multi-instance runtime belongs in this cycle.

## Branch model

```text
main
└─ mosaic/v0.1-foundation              coordinator integration branch
   ├─ parcel/P01-repo-bootstrap
   ├─ parcel/P02-ontology-contracts
   └─ parcel/P03-…
```

One builder owns one parcel/worktree at a time. The coordinator alone merges
parcel branches and updates this board. Do not use a shared implementation
branch until the deterministic spine is integrated and stable.

## Parcel board

| ID | Work | Prereqs | Owns (exclusive) | Status |
|---|---|---|---|---|
| P01 | Go toolchain, local quality gate, and repository bootstrap | — | `go.mod`, `go.sum`, `Taskfile.yml`, `.gitattributes`, `.github/workflows/**`, `cmd/mosaic/**` | ✅ Integrated — `057eaaa` |
| P02 | Authored ontology schemas, generated Go types, contracts, and schema gate | P01 | `ontology/**`, `internal/ontology/**`, `internal/contracts/**`, `cmd/schema-gen/**`, `go.mod`, `go.sum` (schema-validator dependency only) | ✅ Integrated — `289acf9` |
| P03 | SQLite migrations and append-only repositories | P02 | `internal/store/**`, `migrations/**`, `go.mod`, `go.sum` (SQLite driver only) | ✅ Integrated — `e746dfc` |
| P04 | Synthetic dataset manifest, scenario schema, and fixture validator | P02 | `datasets/**`, `internal/dataset/**`, `cmd/datasetgen/**` | ✅ Integrated — `548752d` |
| P05 | Raw ingestion, Luna-result lifecycle, idempotency, and semantic-duplicate links | P02, P03 | `internal/ingestion/**`, `internal/luna/**` | ✅ Integrated — `aa0b659` |
| P06 | Deterministic COP projector, correction handling, checkpoints, and replay | P02, P03 | `internal/state/**`, `internal/replay/**` | ✅ Integrated — `1c53568` |
| P07 | Scenario simulator and replay publisher | P04, P05, P06 | `internal/simulator/**`, `cmd/simulator/**` | ✅ Integrated — `fc236b7` |
| P08 | HTTP/SSE read model and fixed demo-role audit endpoints | P03, P06 | `internal/api/**`, `internal/stream/**` | ✅ Integrated — `06410b8` |
| P09 | Svelte dashboard shell and evidence-aware COP timeline | P08 | `ui/**` | ✅ Integrated — `118eb1d` |
| P10 | Terra structured insight adapter and lifecycle | P03, P06 | `internal/terra/**`, `prompts/terra/**` | ✅ Integrated — `6bc8eaa` |
| P11 | Sol supervisor briefing, recommendation, and audit-action adapter | P03, P06, P08 | `internal/sol/**`, `prompts/sol/**` | ✅ Integrated — `879102d` |
| P12 | End-to-end scenario acceptance suite and local Docker runbook | P07–P11, P14 | `tests/e2e/**`, `Dockerfile`, `docker-compose.yml`, `docs/runbook/**` | 🔒 Claimed — `/root/p12_e2e` |
| P13 | Offline llama.cpp synthetic-data generation and freeze workflow | P02, P04 | `cmd/datasetgen/**`, `internal/datasetgen/**`, `prompts/datasetgen/**`, `docs/dataset-generation.md` | ✅ Integrated — `3213b93` |
| P14 | Executable demo composition root and static UI host | P03, P06, P07, P08, P09 | `cmd/mosaicdemo/**` | ⬜ Ready |
| P15 | Reproducible GoMock tooling and generated contract mocks | P02 | `tools.go`, `internal/contracts/mocks/**`, `go.mod`, `go.sum` | ✅ Integrated — `86caf77` |

## Waves

```text
Wave 0 (serial): P01 → P02
Wave 1:          P03 ∥ P04
Wave 2:          P05 ∥ P06
Wave 3:          P07 ∥ P08
Wave 4:          P09 ∥ P10; then P11
Wave 5:          P14; then P12
Independent:     P13 (offline dataset production; never a runtime dependency)
```

## Parcel acceptance summary

- **P01:** `go test ./...`, `go vet ./...`, and formatting run from one local
  command; CI runs the same command on Linux.
- **P02:** schemas validate; generated code is current; golden fixtures
  round-trip; generated code is not hand-edited.
- **P03:** migrations create immutable event/audit storage and deterministic
  canonical append sequence.
- **P04:** valid generated artifacts include a manifest; invalid schema or
  references are rejected; GGUF/model artifacts are never committed.
- **P05:** exact re-delivery creates no second record or state revision; repaired,
  quarantined, and semantic-duplicate paths preserve provenance.
- **P06:** correction and restart/replay yield the specified COP serialization
  and revision.
- **P07:** the versioned scenario produces its expected event and state timeline.
- **P08:** all read data is evidence-resolvable; only the fixed supervisor role
  can request a briefing or record a decision.
- **P09:** the dashboard distinguishes reported facts, derived assessments, and
  recommendations.
- **P10/P11:** fixture and live adapters validate structured output; a refusal,
  invalid output, or timeout records a model run and changes no state.
- **P12:** a fresh local Docker run completes the end-to-end acceptance scenario through the P14 executable composition root.
- **P14:** one local executable composes the deterministic demo API and the prebuilt dashboard, with no live model or operational-system integration.
- **P15:** `go generate ./internal/contracts/mocks` reproduces the checked-in GoMock implementations from the stable contract interfaces.
- **P13:** a local llama.cpp run writes only a staged candidate dataset with
  complete provenance; validation and explicit freeze are required before a new
  versioned dataset is admitted.

## Shared-file mutexes

| Path | Owner / rule |
|---|---|
| `AGENTS.md`, `HANDOFF.md`, `docs/rfcs/**` | Coordinator only during the cycle |
| `ontology/**`, `internal/contracts/**` | P02; later changes are dedicated contract parcels |
| `cmd/mosaic/**` | P01 initially; composition changes require a dedicated integration parcel |
| `go.mod`, `go.sum`, `Taskfile.yml` | P01; P02 may add only the schema-validator dependency; P03 may add only the SQLite driver; later dependency changes require coordinator approval |
| `cmd/datasetgen/**` | P04 initially; P13 is the dedicated offline-generation extension and adds no module dependency |
| `Mosaic_Architecture_and_Technical_Specification.md` | Product-owner/coordinator approval only |

## Verification command contract

P01 establishes the exact command. Until then, builders run the narrowest
available validation for their parcel and report it verbatim. The final gate is
expected to include formatting, `go vet ./...`, `go test ./...`, schema/codegen
verification, and the end-to-end replay scenario.

## Notes

Format: `YYYY-MM-DD P## <claimed|ready|integrated|blocked> by <owner> — note`.

- 2026-07-18 P00 integrated by coordinator — Git baseline, RFC-0001, agent protocol, and first parcel board established in `7a2e738`.
- 2026-07-18 P01 claimed by `/root/p01_bootstrap` — branch `parcel/P01-repo-bootstrap`.
- 2026-07-18 P01 integrated by coordinator — `057eaaa`; local quality gate passed.
- 2026-07-18 P02 claimed by `/root/p02_contracts` — branch `parcel/P02-ontology-contracts`.
- 2026-07-18 P02 integrated by coordinator — `289acf9`; schema and full quality gates passed.
- 2026-07-18 P03 claimed by `/root/p03_store` — branch `parcel/P03-sqlite-store`; SQLite driver approved.
- 2026-07-18 P04 claimed by `/root/p04_dataset` — branch `parcel/P04-dataset-scenario`.
- 2026-07-18 P03 integrated by coordinator — `e746dfc`; schema and full quality gates passed.
- 2026-07-18 P04 integrated by coordinator — `548752d`; frozen dataset validation and full quality gates passed.
- 2026-07-18 P05 claimed by `/root/p05_ingestion` — branch `parcel/P05-ingestion`.
- 2026-07-18 P06 claimed by `/root/p06_state` — branch `parcel/P06-state`.
- 2026-07-18 P05 integrated by coordinator — `aa0b659`; ingestion and full quality gates passed.
- 2026-07-18 P06 integrated by coordinator — `1c53568`; replay, dataset, and full quality gates passed.
- 2026-07-18 P07 claimed by `/root/p07_simulator` — branch `parcel/P07-simulator`.
- 2026-07-18 P08 claimed by `/root/p08_api` — branch `parcel/P08-api`.
- 2026-07-18 P10 claimed by `/root/p10_terra` — branch `parcel/P10-terra`.
- 2026-07-18 P07 integrated by coordinator — `fc236b7`; in-memory scenario reached revision 9 with all fixture checks true.
- 2026-07-18 P08 integrated by coordinator — `06410b8`; HTTP/SSE and fixed-role audit tests passed.
- 2026-07-18 P10 integrated by coordinator — `6bc8eaa`; structured Terra lifecycle tests passed.
- 2026-07-18 P09 integrated by coordinator — `118eb1d`; evidence-aware Svelte 5 runes dashboard on Vite 8, with authenticated API/SSE and non-operational review controls.
- 2026-07-18 P11 integrated by coordinator — `879102d`; supervisor-only advisory lifecycle validates evidence and records model runs without operational execution.
- 2026-07-18 P13 integrated by coordinator — `3213b93`; staged offline llama.cpp generation, provenance, validation, and explicit immutable freeze are now source-visible.
- 2026-07-18 P12 claimed by `/root/p12_e2e` — Docker acceptance deferred on a new executable composition parcel.
- 2026-07-18 P14 ready by coordinator — required executable composition root; no runtime server will be hidden under the test suite.
- 2026-07-18 P15 integrated by coordinator — `86caf77`; GoMock v0.6.0 is pinned and contract mocks regenerate cleanly.
