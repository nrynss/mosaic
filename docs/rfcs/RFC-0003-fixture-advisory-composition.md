# RFC-0003: Fixture Advisory Composition

- **Status:** Accepted — P21–P23 and P25 integrated; P24 and P26–P28 planned
- **Owner:** Mosaic coordinator
- **Decision date:** 2026-07-19
- **Depends on:** [RFC-0001](RFC-0001-mosaic-demo-foundation.md), [RFC-0002](RFC-0002-public-pluggability-and-agent-observability.md)
- **Implementation snapshot:** P01–P23 and P25 are integrated. The fixture
  advisory contract, deterministic SQLite reader, and bounded public read API
  are complete; P24 and P26–P28 remain unclaimed parcels.

## 1. Decision

Mosaic will next expose the already validated advisory history contained in
the frozen synthetic domestic-disturbance fixture. It does so as an auditable
**fixture replay**, not as a live model invocation and not as a claim that a
current assessment exists.

The fixture defines this timeline:

1. At deterministic COP revision 7, a fixture Terra response produces active
   Insight `insight-domestic-access-001` and a fixture Sol response produces
   Recommendation `recommendation-domestic-001`.
2. At revision 9, the road-opening correction produces
   `insight-domestic-access-001-obsolete`, which immutably supersedes the
   rev-7 Insight.
3. The Recommendation remains an immutable historical record. Because its
   supporting Insight is obsolete, it is not current advice and must never be
   rendered as a current operational recommendation.

The next executable composition will call the existing P10/P11 structured
services only with fixture clients and the fixed, least-privilege inputs
described by the scenario. It will use no network client, model credential,
GGUF, shell tool, or operational-system client.

## 2. Scope

In scope for the increment following P21–P23:

- deterministic, idempotent composition of the frozen Terra/Sol fixture
  history and associated immutable Model Runs/Audit Records;
- an additive advisory-history read contract and a SQLite implementation;
- a bounded public advisory read model and dashboard presentation of cited
  historical artifacts and their current/superseded state; and
- local executable, E2E, Docker, and runbook proof.

Out of scope:

- live OpenAI or other model transport, runtime model selection, credentials,
  streaming model calls, or automatic assessment of arbitrary incoming data;
- autonomous reconciliation, multi-instance deployment, PostgreSQL, shared
  notification, or a durable outbox;
- login, authorization, privacy/retention automation, raw payload display,
  and all external operational actions.

## 3. Durable and safety semantics

All fixture-created Insights, Recommendations, Model Runs, and Audit Records
are append-only. The rev-9 obsolescence is a new Insight record; it must not
edit or delete the rev-7 Insight. The deterministic projector remains the only
mutator of source-derived COP state.

Fixture composition must have named, fixed record identifiers and use the
existing structured service validation for Terra/Sol outputs. It must be safe
to run again against an intact SQLite volume: no duplicate advisory, Model Run,
or fixture audit record may be appended. A partially written fixture advisory
sequence is an integrity failure that must stop composition and be visible to
the caller; it must not be silently overwritten, deleted, or repaired by an
LLM.

Every public review remains an immutable audit write with `executed: false`.
No fixture artifact authorizes dispatch, contact, external state mutation, or
an operational decision.

## 4. Required read contract

P22 adds an additive `contracts.AdvisoryHistoryReader` plus
`contracts.AdvisoryHistory`. It returns only persisted advisory-domain records:

- Insights;
- Recommendations;
- Terra/Sol Model Runs; and
- Audit Records.

The contract deliberately does not expose Raw Events, Canonical Events, raw
payload bytes, checksums, prompts, model responses, secrets, credentials, or
an operational command. It is neither an HTTP response nor a generic record
export. Later HTTP code will map this domain snapshot to a smaller public
representation.

P23 implements the contract as a read-only SQLite adapter. No migration is
needed: P03 already persists all four record classes as immutable JSON.
Ordering must be deterministic, and decode/query errors must fail closed.

## 5. User-visible state language

The advisory read model and dashboard must use these terms precisely:

| State | Meaning |
|---|---|
| **Historical** | An immutable fixture artifact evaluated at an earlier COP revision. |
| **Current** | An active cited assessment for the recovered revision, when such an artifact exists. |
| **Superseded** | A later immutable Insight explicitly obsoletes a prior Insight. |
| **Not current** | A Recommendation whose cited Insight is superseded or whose revision is older than the recovered COP. |
| **Fixture-composed** | A checked-in structured fixture was validated and persisted locally; it is not live model transport. |
| **Unavailable** | A live model transport is not composed, or a structured fixture response was refused, invalid, or failed. |

The dashboard must not infer an assessment from COP facts. It may show an
artifact only after a bounded API response resolves it. It must visually make
the final rev-9 state clear: this fixture has historical advisory evidence, but
no current Terra assessment or Sol recommendation.

## 6. Compatibility and migration

The contract is additive and has no persistence migration. Existing callers
remain valid. SQLite stays the local default and all existing P01–P20 public
routes retain their behavior. A later public advisory endpoint is a new,
versioned route and retains RFC-0002's public actor/policy seam.

The existing P04 fixture is the sole source for advisory IDs, text, evidence,
and lifecycle state. No new synthetic data needs to be generated and no model
inference should occur during a normal build or test.

## 7. Parcel plan and acceptance sequence

P21–P23 are integrated. The remaining parcels are independently claimable only
when their listed prerequisites are integrated on `HANDOFF.md`.

| Parcel | Purpose | Prereqs | Exclusive ownership | Acceptance boundary |
|---|---|---|---|---|
| P21 | Archive the completed v0.1 board, establish RFC-0003, and create the new live handoff. | P20 | Coordinator docs/board paths | New cycle is externally handoff-ready. |
| P22 | Add `AdvisoryHistoryReader` and generated mocks. | P21 | `internal/contracts/**` | Additive bounded contract compiles and regenerates. |
| P23 | Implement the read-only SQLite advisory-history adapter. | P22 | `internal/store/**` | Terra/Sol filtering, actual-instant ordering, empty history, and fail-closed decode/timestamp behavior. |
| P24 | Replay the frozen Terra/Sol advisory lifecycle through P10/P11 fixture clients. | P22, P23 | `internal/simulator/**` | Fresh/restart-safe immutable fixture history; no network or COP mutation. |
| P25 | Add the bounded public advisory read endpoint and accurate capability status. | P22, P23 | `internal/api/**` | Public evidence-backed history with no raw/model-response leakage. |
| P26 | Render the bounded advisory history and supersession state. | P25 | `ui/**` | Rev-7 advice is visibly historical/not current at revision 9. |
| P27 | Compose P24/P25 into local executable startup. | P24, P25 | `cmd/mosaicdemo/**` | Fresh and retained-volume startup are idempotent and fixture-only. |
| P28 | Prove the public API/UI/Docker/runbook boundary end-to-end. | P26, P27 | `tests/e2e/**`, `docs/runbook/**` | No-header public proof, supersession proof, restart proof, and Docker smoke. |

The planned waves are:

```text
P21 → P22 → P23
P24 ∥ P25
P26 (after P25) ∥ P27 (after P24 and P25)
P28 (after P26 and P27)
```

### P24 fixture replay acceptance

P24 consumes only the frozen `domestic-disturbance` fixture and the P22/P23
interfaces. It uses the existing P10 Terra and P11 Sol services with local
fixture clients, fixed clocks, and deterministic fixture Model Run identifiers.
It must preserve this sequence:

1. validate/persist the active rev-7 Terra Insight;
2. append the fixture's immutable briefing-request Audit Record;
3. validate/persist the rev-7 Sol Recommendation for `supervisor-demo`;
4. validate/persist the rev-9 Terra obsolescence Insight; and
5. append the fixture's immutable supervisor acknowledgement Audit Record.

The replay must use rev-7 and rev-9 COP snapshots from the deterministic
scenario timeline, not infer state from the final COP. It must use transactions
for each structured Model Run/output pair, recognize an intact replay on
restart without appending duplicates, and fail closed on a partial fixture
stage. It may not alter source-derived COP state, fixture data, ontology,
P10/P11 source, migrations, or create any live transport.

### P25 public advisory acceptance

P25 adds one public, versioned `GET /api/v1/advisories` route and its policy
action. It receives only `contracts.AdvisoryHistoryReader` plus recovered COP
state through API configuration. The response may expose evidence-cited
Insight/Recommendation artifacts and bounded lifecycle/composition status, but
must not expose Model Run prompts/responses, raw payloads, checksums,
credentials, or generic Audit Record history. It must classify the fixture's
final state as historical/superseded/not current, not as current advice.

The operations receipt must report `fixture_composed` only when composition
explicitly supplies that mode; the API default stays unavailable for fixture
advisory composition. Public no-header access and a replaceable deny policy
must both be tested. Existing routes retain their behavior.

### P26–P28 presentation and executable acceptance

P26 renders only the P25 bounded response. It must offer evidence resolution
and prefill a permitted immutable review target without inventing a new action.
It must show the rev-7 recommendation as not current after rev-9 and retain the
explicit absence of live Terra/Sol transport.

P27 composes the frozen scenario, P24 replay, P23 history reader, and P25 API
before serving the existing dashboard. A fresh SQLite file and a retained
SQLite volume must produce one identical fixture history; startup must make no
network request and may not silently repair a partial advisory sequence.

P28 proves no-header public endpoint/UI behavior, bounded-field exclusion,
rev-9 supersession language, immutable `executed: false` public review writes,
restart idempotency, UI check/build, and the fresh isolated Docker smoke. The
Docker image continues to contain no model binary, live model client, or
operational-system client.
## 8. Non-negotiable boundary

This RFC does not make Mosaic a live advisor. It makes a frozen synthetic
advisory history legible and auditable. AI-derived records remain evidence
backed, schema-valid, immutable, and non-operational; they cannot mutate the
COP or execute an action.
