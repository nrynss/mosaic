# RFC-0004: Internal Domain Profile Separation

- **Status:** Proposed
- **Owner:** Mosaic coordinator
- **Decision date:** 2026-07-20
- **Depends on:** RFC-0001, RFC-0002, RFC-0003

## Decision

Mosaic remains an internal developer-tool foundation, not a public SDK or a runtime plugin host. Its reusable core must not name the domestic-disturbance reference scenario, police roles, roads, or emergency-service concepts.

The domestic-disturbance scenario becomes an internal **domain profile**. A profile owns its fixture validation, deterministic projection rules, fixture normalizer/advisory replay, state-fact resolution, and reference composition. The reusable core retains append-only persistence, ingestion lifecycle, deterministic replay orchestration, immutable model/audit records, bounded API plumbing, and the UI/API boundary.

The existing Svelte application remains a separate API client. Its build and static serving are supplied at composition time; it must not import domain packages or database code.

## Scope

This increment introduces an internal `DomainProfile` composition seam and moves the domestic-disturbance-specific code behind it. The existing demo stays the only registered profile and must produce byte-identical observable results: ten synthetic source events, final COP revision 9, the fixture advisory lifecycle, bounded public API responses, and `executed: false` review writes.

This increment does not add a public Go module, dynamic plugin loading, arbitrary-schema support, a second domain, live data/model transport, identity, or operational actions.

## Contract proposal

Add an additive internal profile contract with these responsibilities:

- validate the selected frozen assets;
- compose a deterministic scenario/recovery runner and fixture advisory replay;
- resolve domain state facts for the bounded evidence read model;
- describe the profile identifier used by the executable composition root; and
- supply the demo actor identities the generic host composes into the public
  actor resolver and the Sol briefing guard.

`cmd/mosaicdemo` selects its profile explicitly and receives the UI directory as configuration. It may know the registered profile name, but it may not directly reference a fixture directory, domestic event identifier, police role, or domain reducer.

`internal/api` continues to resolve persisted immutable artifacts itself. It delegates only `state_fact` interpretation to the selected profile, preserving the API's bounded serialization and raw-payload exclusion.

### Identity decoupling

The scenario's actor identities (`viewer-demo`, `supervisor-demo`) are domain
data, not core constants. They remain present in the frozen dataset's audit
records as observable output, but the reusable core packages must not name them:

- `internal/sol` takes the authorized briefing requester as a `RequiredRequester`
  configuration value rather than a hardcoded identity. The reusable service
  validates against the configured value; the profile supplies it.
- `internal/api`'s `PublicActorResolver` carries the viewer/supervisor identity
  tokens as configurable fields. A zero-value resolver never elevates a request.
  The composition root wires the tokens from the selected profile's identities.

This keeps the identity-header display feature and Sol's supervisor-only guard
byte-identical in the composed demo while removing the domain literals from the
core Go packages.

## Migration and safety

This is a source-layout and composition refactor. SQLite schema, persisted record shapes, datasets, HTTP routes, advisory lifecycle, replay order, and UI wire protocol remain unchanged. No migration is required. The synthetic domestic-disturbance fixtures remain the sole executable profile.

## Acceptance

- Core packages contain no domestic-disturbance IDs, `supervisor-demo`, or police-domain state-fact collection names. The value survives only as frozen dataset data and profile-supplied composition configuration.
- The reference profile owns the domestic fixture validator, deterministic reducer, fixture simulation/advisory replay, state-fact resolver, and demo actor identities.
- `mosaicdemo` composes one explicit profile plus one separately configured UI asset directory, without direct domestic-domain imports.
- Existing simulator, API, UI, E2E, Docker, dataset, replay, and quality checks preserve the current observable demo behavior.

## Product direction and non-claims

The reference UI should become incident-centred: a synthetic intake identifier, elapsed counter, evidence-backed context, an Analyze affordance, recipient-specific handoff cards, and a provenance/actions tab. The current connection/health panel is developer diagnostics and belongs in a compact status view.

A future recurrent-issue feature may deterministically surface prior recorded handoffs for the same configured area after a configurable window. It may prepare a pending note for review and expose it to a separate local feed. It must not imply autonomous external contact, multi-instance delivery, or LLM self-healing; current and near-term actions remain immutable records with executed: false.
