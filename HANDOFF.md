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
| P01 | Go toolchain, local quality gate, and repository bootstrap | — | `go.mod`, `go.sum`, `Taskfile.yml`, `.gitattributes`, `.github/workflows/**`, `cmd/mosaic/**` | ⬜ Ready |
| P02 | Authored ontology schemas, generated Go types, contracts, and schema gate | P01 | `ontology/**`, `internal/ontology/**`, `internal/contracts/**`, `cmd/schema-gen/**` | ⬜ Ready |
| P03 | SQLite migrations and append-only repositories | P02 | `internal/store/**`, `migrations/**` | ⬜ Ready |
| P04 | Synthetic dataset manifest, scenario schema, and fixture validator | P02 | `datasets/**`, `internal/dataset/**`, `cmd/datasetgen/**` | ⬜ Ready |
| P05 | Raw ingestion, Luna-result lifecycle, idempotency, and semantic-duplicate links | P02, P03 | `internal/ingestion/**`, `internal/luna/**` | ⬜ Ready |
| P06 | Deterministic COP projector, correction handling, checkpoints, and replay | P02, P03 | `internal/state/**`, `internal/replay/**` | ⬜ Ready |
| P07 | Scenario simulator and replay publisher | P04, P05, P06 | `internal/simulator/**`, `cmd/simulator/**` | ⬜ Ready |
| P08 | HTTP/SSE read model and fixed demo-role audit endpoints | P03, P06 | `internal/api/**`, `internal/stream/**` | ⬜ Ready |
| P09 | Svelte dashboard shell and evidence-aware COP timeline | P08 | `ui/**` | ⬜ Ready |
| P10 | Terra structured insight adapter and lifecycle | P03, P06 | `internal/terra/**`, `prompts/terra/**` | ⬜ Ready |
| P11 | Sol supervisor briefing, recommendation, and audit-action adapter | P03, P06, P08 | `internal/sol/**`, `prompts/sol/**` | ⬜ Ready |
| P12 | End-to-end scenario acceptance suite and local Docker runbook | P07–P11 | `tests/e2e/**`, `Dockerfile`, `docker-compose.yml`, `docs/runbook/**` | ⬜ Ready |

## Waves

```text
Wave 0 (serial): P01 → P02
Wave 1:          P03 ∥ P04
Wave 2:          P05 ∥ P06
Wave 3:          P07 ∥ P08
Wave 4:          P09 ∥ P10; then P11
Wave 5:          P12
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
- **P12:** a fresh local Docker run completes the end-to-end acceptance scenario.

## Shared-file mutexes

| Path | Owner / rule |
|---|---|
| `AGENTS.md`, `HANDOFF.md`, `docs/rfcs/**` | Coordinator only during the cycle |
| `ontology/**`, `internal/contracts/**` | P02; later changes are dedicated contract parcels |
| `cmd/mosaic/**` | P01 initially; composition changes require a dedicated integration parcel |
| `go.mod`, `go.sum`, `Taskfile.yml` | P01; later dependency changes require coordinator approval |
| `Mosaic_Architecture_and_Technical_Specification.md` | Product-owner/coordinator approval only |

## Verification command contract

P01 establishes the exact command. Until then, builders run the narrowest
available validation for their parcel and report it verbatim. The final gate is
expected to include formatting, `go vet ./...`, `go test ./...`, schema/codegen
verification, and the end-to-end replay scenario.

## Notes

Format: `YYYY-MM-DD P## <claimed|ready|integrated|blocked> by <owner> — note`.

- 2026-07-18 P00 integrated by coordinator — Git baseline, RFC-0001, agent
  protocol, and first parcel board established in `7a2e738`; P01 is next.
