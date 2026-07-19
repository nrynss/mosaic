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

The integration branch is `mosaic/v0.1-foundation`. This increment begins from
`c03ba39` (`docs: reconcile public operations increment`), after P01–P20 and
their full quality/Docker proof.

Every implementation parcel uses a new isolated branch and worktree from the
latest integrated `mosaic/v0.1-foundation` SHA:

```text
mosaic/v0.1-foundation
├─ parcel/P21-fixture-advisory-rfc
├─ parcel/P22-advisory-history-contracts
└─ parcel/P23-advisory-history-store
```

Do not reuse a prior P01–P20 worktree. Do not claim a row until all of its
prerequisites are marked `✅ Integrated` on this board.

## Parcel board

| ID | Work | Prereqs | Owns (exclusive) | Status |
|---|---|---|---|---|
| P21 | Re-baseline the handoff and record RFC-0003's fixture-only advisory decision | P20 | `HANDOFF.md`, `docs/archive/HANDOFF-v0.1-foundation.md`, `docs/rfcs/RFC-0003-fixture-advisory-composition.md` | ✅ Integrated — `48d96c8` |
| P22 | Add the additive, read-only advisory-history contract and regenerate checked-in GoMock output | P21 | `internal/contracts/**` | ✅ Integrated — `17a4cde` |
| P23 | Implement deterministic SQLite reads for the P22 advisory history contract; no migration | P22 | `internal/store/**` | ✅ Integrated — `8cbc905` |

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

## Shared-file mutexes

| Path | Owner / rule |
|---|---|
| `AGENTS.md`, `HANDOFF.md`, `docs/rfcs/**`, `docs/archive/**` | Coordinator / P21 only for this increment |
| `internal/contracts/**` | P22 only; every cross-package shape is additive and generated mocks stay current |
| `internal/store/**` | P23 only; no migrations or schema edits are permitted |
| `ontology/**`, `internal/ontology/**`, `migrations/**`, `go.mod`, `go.sum`, `Taskfile.yml` | Frozen for P21–P23 |

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

## Future sequence — not yet claimable

After P23, the coordinator will open dedicated parcels for fixture advisory
replay, the bounded public advisory API, dashboard presentation, executable
composition, and end-to-end/Docker proof. Those parcels are deliberately not
pre-claimed here: their exact paths and acceptance criteria depend on the P22
contract and P23 reader as integrated, not merely proposed, shapes.

## Notes

Format: `YYYY-MM-DD P## <claimed|ready|integrated|blocked> by <owner> — note`.

- 2026-07-19 P21 claimed by coordinator — base `c03ba39`, branch `parcel/P21-fixture-advisory-rfc`, worktree `.worktrees/P21-fixture-advisory-rfc`; archive completed v0.1 handoff and define the next external-builder-ready parcels.
- 2026-07-19 P21 integrated by coordinator — `48d96c8`; archived the completed v0.1 board, created RFC-0003, and released P22 for an isolated external-builder claim.
- 2026-07-19 P22 claimed by coordinator — base `4b9a69e`, branch `parcel/P22-advisory-history-contracts`, worktree `.worktrees/P22-advisory-history-contracts`; additive advisory-history contract and regenerated GoMock output only.
- 2026-07-19 P22 integrated by coordinator — `17a4cde`; reviewed the additive contract/mock change and reran `go run ./cmd/mosaic quality` successfully.
- 2026-07-19 P23 claimed by coordinator — base `bec2744`, branch `parcel/P23-advisory-history-store`, worktree `.worktrees/P23-advisory-history-store`; deterministic SQLite advisory-history reads only, with no migrations.
- 2026-07-19 P23 integrated by coordinator — `8cbc905`; bounded read-only SQLite history now filters Terra/Sol Model Runs, orders real RFC-3339 instants deterministically, and fails closed for selected corrupt records; full quality passed.
