# RFC-0003: Fixture Advisory Composition

- **Status:** Accepted — P21 design; P22/P23 implementation prerequisites
- **Owner:** Mosaic coordinator
- **Decision date:** 2026-07-19
- **Depends on:** [RFC-0001](RFC-0001-mosaic-demo-foundation.md), [RFC-0002](RFC-0002-public-pluggability-and-agent-observability.md)
- **Implementation snapshot:** P01–P20 integrated; P21 establishes the
  contract and SQLite read prerequisites for the next fixture-only increment.

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

## 7. Acceptance sequence

P21–P23 establish the prerequisites:

1. Archive the completed v0.1 handoff, create a fresh live handoff, and record
   this decision.
2. Add/regenerate the advisory-history contract and mock.
3. Add deterministic SQLite reads over the existing immutable records with no
   migration.

The coordinator then defines the follow-on composition/API/UI/acceptance
parcels against the integrated P22/P23 shapes. The final increment acceptance
will prove fresh and restart-safe fixture composition, public bounded reads,
supersession language, immutable `executed: false` review writes, and the full
quality/Docker gate.

## 8. Non-negotiable boundary

This RFC does not make Mosaic a live advisor. It makes a frozen synthetic
advisory history legible and auditable. AI-derived records remain evidence
backed, schema-valid, immutable, and non-operational; they cannot mutate the
COP or execute an action.
