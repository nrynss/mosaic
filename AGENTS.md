# Mosaic Agent Protocol

This file is binding for every human or coding agent working in this repository.
It governs the implementation workflow; [`docs/rfcs/RFC-0001-mosaic-demo-foundation.md`](docs/rfcs/RFC-0001-mosaic-demo-foundation.md)
governs the first demo's design; [`HANDOFF.md`](HANDOFF.md) is the live task board.

## Read order

Before changing a file, read these in order:

1. This file in full.
2. The current RFC and any ADR cited by the parcel.
3. `HANDOFF.md` in full, including the current parcel board and notes.
4. The claimed parcel and every prerequisite parcel.
5. The contracts, schemas, package documentation, and tests used by the parcel.

Pre-flight assertion: *I own every path I will edit, the cross-package shapes I
need already exist, and I can validate this parcel independently.* If any part
is false, stop and propose a contract change.

## Roles and ownership

- The **coordinator** owns integration, the parcel board, cross-lane arbitration,
  shared composition roots, and the full verification gate.
- A **builder** owns only the paths listed in its claimed parcel. Unlisted files
  are off limits, except explicitly named one-line integration seams.
- A parcel is atomic only when its path ownership and acceptance test are both
  independent. Small but overlapping work is not atomic.
- Builders do not integrate their own work. They return a commit SHA and exact
  validation output; the coordinator changes the parcel to `✅ Integrated` only
  after merging and rerunning the complete gate.

## Contracts first

- Authored JSON Schemas in `ontology/` are the source of truth.
- Generated Go types in `internal/ontology/gen/` are checked in and are never
  edited by hand.
- `internal/contracts/` contains the stable interfaces and cross-package
  contracts. A package may consume a contract but may not invent a cross-package
  field, type, or interface locally.
- Schema and contract changes require a written proposal naming the change, why,
  impacted owners, migration behaviour, and acceptance test. The coordinator
  lands approved contract changes as their own focused commit.
- Contract changes are additive by default. A breaking change requires an RFC or
  ADR update plus adapters or a migration in the same integration pass.

## Safety and data rules

- The demo uses synthetic data only. Do not add real operational records, PII,
  API keys, tokens, or credentials to the repository.
- `models/`, GGUF artifacts, databases, and local environment files are ignored.
- Raw Events, Canonical Events, Insights, Recommendations, model runs, and audit
  records are immutable. Corrections create superseding records; history is never
  rewritten.
- The deterministic projector is the only mutator of the operational projection.
  AI output may assess the projection but cannot issue an operational action.
- Luna, Terra, and Sol output must validate against their versioned schemas and
  cite evidence. Invalid, refused, or unavailable model output is recorded and
  produces no state mutation.

## Worktree and claim protocol

During the greenfield foundation cycle, every implementation parcel uses an
isolated branch and worktree from `mosaic/v0.1-foundation`. The coordinator keeps
the board current to avoid concurrent edits to `HANDOFF.md`.

1. Claim only an `⬜ Ready` parcel whose prerequisites are `✅ Integrated`.
2. The coordinator records the claim, base SHA, branch, worktree, and owner.
3. Work only within the parcel's `Owns` paths. Rebase only immediately before
   handoff; if an unrelated conflict appears, stop rather than repairing another
   parcel.
4. Commit one focused parcel change, run its acceptance checks, and hand off its
   SHA and exact results.
5. The coordinator integrates and updates the board. A blocked builder unclaims
   the parcel through the coordinator with a one-line reason.

Status flow: `⬜ Ready → 🔒 Claimed → 🟡 Ready for integration → ✅ Integrated`.
`⛔ Blocked` and `↩ Unclaimed` are explicit states, not silent abandonment.

## Definition of done

A parcel is complete only when it:

- satisfies its stated acceptance criteria;
- has focused tests and preserves existing tests;
- passes formatting, static analysis, package tests, and the relevant contract or
  replay checks;
- keeps generated artifacts current;
- has no unrelated changes, secrets, or model binaries; and
- records any changed persistence, replay, or migration behaviour.

For this demo, latency is observed rather than release-gated. Deterministic
validation, projection, replay, evidence resolution, and auditability are
release gates.
