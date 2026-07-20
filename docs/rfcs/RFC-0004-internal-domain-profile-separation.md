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
- resolve domain state facts for the bounded evidence read model; and
- describe the profile identifier used by the executable composition root.

`cmd/mosaicdemo` selects its profile explicitly and receives the UI directory as configuration. It may know the registered profile name, but it may not directly reference a fixture directory, domestic event identifier, police role, or domain reducer.

`internal/api` continues to resolve persisted immutable artifacts itself. It delegates only `state_fact` interpretation to the selected profile, preserving the API's bounded serialization and raw-payload exclusion.

## Migration and safety

This is a source-layout and composition refactor. SQLite schema, persisted record shapes, datasets, HTTP routes, advisory lifecycle, replay order, and UI wire protocol remain unchanged. No migration is required. The synthetic domestic-disturbance fixtures remain the sole executable profile.

## Acceptance

- Core packages contain no domestic-disturbance IDs, `supervisor-demo`, or police-domain state-fact collection names.
- The reference profile owns the domestic fixture validator, deterministic reducer, fixture simulation/advisory replay, and state-fact resolver.
- `mosaicdemo` composes one explicit profile plus one separately configured UI asset directory, without direct domestic-domain imports.
- Existing simulator, API, UI, E2E, Docker, dataset, replay, and quality checks preserve the current observable demo behavior.
