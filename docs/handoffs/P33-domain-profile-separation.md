# P33 builder handoff — internal domain-profile separation

## Purpose

Complete [`RFC-0004`](../rfcs/RFC-0004-internal-domain-profile-separation.md).
Mosaic is an internal framework, not a public SDK or a plugin runtime. The
domestic-disturbance scenario is its single synthetic reference profile.

P33 deliberately has no visible UI redesign. It makes the forthcoming
operator workspace safe to build without binding the reusable host or API to
the reference domain.

## Clean starting point

- Base integration commit: `d239387`.
- Create a new isolated branch/worktree from the current
  `mosaic/v0.1-foundation` integration branch. Suggested branch:
  `parcel/P33-domain-profile-completion`.
- Do **not** use `parcel/P33-domain-profile-separation` as a code base. That
  abandoned coordinator worktree contains incomplete, uncommitted moves and
  failed tests. It is not accepted work and must not be merged or copied from.
- Read `AGENTS.md`, RFC-0001 through RFC-0004, `HANDOFF.md`, and this file in
  full before editing.

## Exclusive ownership

Only these paths belong to P33:

- `internal/profile/**`
- `internal/reference/domesticdisturbance/**`
- `internal/reference/registry/**`
- `internal/dataset/**`, `internal/simulator/**`, `internal/state/**`
- `internal/api/evidence.go`, `internal/api/server_test.go`
- `cmd/mosaicdemo/**`, `cmd/simulator/**`, `cmd/datasetgen/**`
- `tests/e2e/**`

Do not modify `ui/**`, `ontology/**`, `internal/ontology/**`,
`internal/contracts/**`, `internal/store/**`, `migrations/**`, `go.mod`,
`go.sum`, `Dockerfile`, `docker-compose.yml`, datasets, prompts, or any
coordinator document. The coordinator owns the board and RFCs.

## Required implementation

1. Introduce an internal `Profile`/`Runtime` seam in `internal/profile/`.
   `Profile` identifies and validates one profile, then composes a `Runtime`.
   `Runtime` provides deterministic recovery, evidence resolution, and a
   startup `Run(context.Context)` operation. It remains internal; do not make
   a public module, dynamic loader, or generic plugin protocol.

2. Move the domestic fixture validator, projector, fixture simulator, fixture
   advisory replay, and state-fact interpretation under
   `internal/reference/domesticdisturbance/`. Update affected imports and test
   paths. From the moved packages, the repository root is four parent
   directories above the package, not two.

3. Add an internal registry for the one bundled reference profile. Unknown
   profile IDs must fail clearly. The profile validates only its frozen assets,
   composes replay/advisory recovery, preserves the retained-volume rev-7/rev-9
   advisory fallback, and resolves its `state_fact` evidence.

4. Make `internal/api/evidence.go` domain-neutral. It keeps persisted immutable
   artifact resolution. It delegates only `state_fact` interpretation to an
   optional API-local `StateFactResolver` supplied by the selected profile.
   Without that resolver, state-fact evidence is explicitly unresolved.

5. Refactor `cmd/mosaicdemo` into the generic host. It selects a registered
   profile explicitly (for example `MOSAIC_PROFILE`), validates/composes/runs
   it, supplies its runtime to the API, and keeps `MOSAIC_UI_DIR` independent.
   `mosaicdemo` must not directly import a domestic package, fixture directory,
   domestic event ID, police role, or reducer. The reference-only `datasetgen`
   and `simulator` CLIs may import reference packages.

6. Preserve all existing external behaviour: ten source events, final COP
   revision 9, fixture-composed advisory lifecycle, bounded API/evidence
   responses, and immutable `executed: false` review writes.

## Constraints

- No schema, migration, contract, persistence, UI, Docker, dependency, live
  model, credential, identity, or external-action change.
- No real data or credentials in the repository.
- The deterministic projector remains the sole source-derived COP mutator.
- State-fact delegation must not weaken raw-payload omission or bounded
  persisted-artifact resolution.
- Do not rewrite immutable records to make retained startup succeed.

## Acceptance and evidence

Run and return raw output for:

```text
gofmt -w <only changed Go files>
go vet ./...
go test ./...
go run ./cmd/mosaic quality
cd ui && npm run check && npm run build
```

Run this focused negative scan; it must return no matches:

```text
rg -n "domestic-disturbance|supervisor-demo|incidents|weather_alerts" internal/api internal/profile cmd/mosaicdemo
```

Run the E2E suite as well. It must preserve no-header access, revision 9,
fixture advisory supersession, evidence resolution, and non-operational audit
writes.

## Return format

```text
Parcel: P33
Base integration SHA: <SHA>
Branch / worktree: <branch> / <worktree>
Owned paths changed: <only paths listed above>
Commit SHA: <one focused commit>
Validation commands and results:
<verbatim output>
Unrelated changes: none
```

Do not edit `HANDOFF.md`, RFCs, or this file from the builder worktree. The
coordinator will inspect ownership, merge, rerun the full quality and
Docker/E2E proof, and then mark P33 integrated.

## Queued after P33

The following are blocked and not part of P33: additive operator-handoff
schema/contract; immutable handoff persistence/provenance reader;
profile-backed synthetic simulation sessions; workflow API; incident-command
UI; E2E/Docker/demo proof; and the optional server-side OpenAI transport with
an environment-only key, budget/call caps, schema validation, citations, and
fixture fallback.
