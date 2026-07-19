# RFC-0002: Public Demo Pluggability and Agent Observability

- **Status:** Implemented — P17–P20 public operations increment
- **Owner:** Mosaic coordinator
- **Decision date:** 2026-07-19
- **Depends on:** [RFC-0001](RFC-0001-mosaic-demo-foundation.md)
- **Implementation snapshot:** Public actor/policy seam, bounded operations API, Svelte operations receipt, executable composition, public E2E acceptance, and Docker smoke are integrated.

## 1. Decision

Mosaic remains openly accessible for the demo. It will not implement
authentication, authorization, production privacy controls, or retention
automation at this stage.

The codebase must nevertheless keep those concerns *pluggable*: public access
is a configured default, not an assumption spread through HTTP handlers,
business services, or persistence. Likewise, SQLite remains the correct
single-instance demo store, while the event, evidence, and dispatch boundaries
must be able to acquire a shared-store implementation later.

The next visible product capability is an evidence-backed **agent operations
view**. It must show what each agent-like stage did, what it can safely do,
what it could not do, and how deterministic recovery behaved. It must never
claim that an unimplemented operation healed itself.

## 2. Scope and non-goals

In scope:

- A public-by-default actor and policy seam.
- Explicit future boundaries for a shared PostgreSQL store and multi-instance
  dispatch.
- A small observability surface that makes ingestion, validation, projection,
  recovery, assessment, recommendation, and audit behavior understandable.
- Clear status language for implemented recovery versus future reconciliation.
- Text-only privacy and retention boundaries for the synthetic demo.

Out of scope:

- Login, tokens, sessions, RBAC, user management, or identity-provider setup.
- Bundling PostgreSQL in the demo container or migrating the local demo away
  from SQLite.
- A live model client, autonomous operational action, or raw-payload display.
- Production SLIs, paging, retention jobs, data classification, or legal policy.

## 3. Public access with a pluggable future

### 3.1 Current policy

The demo is public. `X-Mosaic-Demo-Identity` is optional viewer/supervisor
*display metadata*, not authentication and not a security boundary. Any client
may omit it or choose either fixed mode; the public actor/policy defaults permit
all current demo reads and immutable audit-record writes.

### 3.2 Required seam

HTTP composition will supply an `ActorResolver` and an `ActionPolicy` to the
API layer:

```text
request -> ActorResolver -> Actor { id, labels, source }
                         -> ActionPolicy -> allow | deny | audit reason
```

The demo defaults are:

- `PublicActorResolver`: returns `public-demo` for every request; it may retain
  the requested demo mode as display metadata.
- `AllowDemoPolicy`: allows demo reads and non-operational audit-record writes.
  It never changes the invariant that every endpoint returns `executed: false`.

A later deployment may replace those two adapters with an identity-aware
resolver and policy. Domain services receive a resolved actor/policy result,
not HTTP headers, tokens, or provider-specific claims. No persistence schema
or handler should rely on a particular identity provider.

## 4. Why PostgreSQL is deferred

PostgreSQL has no role in the current local demo. SQLite provides one durable
file, deterministic tests, and a simple Docker volume for one process.

PostgreSQL becomes useful only when multiple Mosaic processes need to share a
durable event log, projection receipts, checkpoints, model records, and audit
records. It is not, by itself, a multi-instance design. A shared deployment
also requires durable dispatch, cross-instance notification, and a clear owner
for projection/reconciliation.

The future store adapter must preserve the current durable semantics:

- database-assigned canonical append order;
- immutable records and correction chains;
- idempotent raw-source delivery;
- atomic projection receipt/checkpoint commit;
- evidence resolution against persisted artifacts; and
- deterministic replay from checkpoint plus canonical events.

`SQLiteEvidenceResolver` and the in-process SSE broker are local adapters,
not cross-instance contracts. Their replacements must be selected by
composition; callers continue to depend on evidence and stream interfaces.

## 5. Multi-instance path

Before scaling beyond one process, Mosaic needs a dedicated design for:

1. A shared PostgreSQL-backed store adapter behind the existing repository
   contracts, plus a database-capability seam for evidence reads.
2. A durable outbox or work table written with the Canonical Event, so a
   committed event cannot be stranded after post-commit dispatch failure.
3. A projector ownership/lease model. One canonical event must yield one
   projection receipt and checkpoint revision even when workers retry or race.
4. A shared event notification mechanism for SSE fan-out. In-memory broker
   notices remain best-effort and process-local.
5. A reconciliation worker that discovers committed-but-unprojected events,
   retries deterministic projection, and records a bounded outcome.

This is the future meaning of **self-healing**. It is a deterministic,
auditable reconciliation process—not an LLM deciding how to repair state.

## 6. Agent operations view

The observability surface should be a small public dashboard panel and a
versioned read endpoint. It derives its facts from persisted records and
deterministic state; it must not expose raw `payload_bytes_b64`, raw checksums,
prompts, model responses, secrets, or user data.

### 6.1 Capability and status matrix

| Stage | User-visible feature | Evidence to show | Current status |
|---|---|---|---|
| Source intake | Observes synthetic source events | raw-event count, source IDs, latest receipt time | Implemented |
| Luna normalization | Accepts, repairs, quarantines, or rejects a source | lifecycle counts; linked Luna Result and Model Run | Implemented with fixture adapter |
| Deterministic projector | Updates the COP from canonical history | state revision, checkpoint/receipt, canonical sequence | Implemented |
| Restart recovery | Rebuilds the COP without duplicating fixture history | replay result, recovered revision, idempotency outcome | Implemented for startup/replay |
| Reconciliation | Finds and resolves stranded projection work | pending/attempted/succeeded/failed reconciliation records | Future; do not present as active today |
| Terra assessment | Produces a cited assessment | Model Run outcome, evidence count, state revision | Service implemented; not live-composed |
| Sol advisory | Produces a supervisor-review option | Model Run outcome, recommendation/evidence links | Service implemented; not live-composed |
| Human review | Records a non-operational review | immutable Audit Record, `executed: false` | Implemented |
| Operational action | Dispatches or mutates an external system | none | Permanently unavailable in this demo |

### 6.2 Implemented public telemetry

The current endpoint returns only bounded values for one observation:

- service version, start time, uptime, observed time, latest source receipt,
  and same-request recovered COP revision/projected time;
- raw/canonical/projected/unprojected/checkpoint/insight/recommendation/audit
  counts, Luna lifecycle counts, and Model Run outcomes grouped by agent and
  validation status;
- active local SSE connections plus latest published event name and timestamp;
  and
- exact capability mode/status records: fixture, composed, recovered,
  unavailable, or permanently unavailable.

The dashboard derives its compact recovered/degraded/unavailable presentation
from those records. `unprojected_events` becomes a degraded observation; it is
not evidence that a reconciliation worker exists. Raw payload bytes, checksums,
prompts, model responses, secrets, and user data are excluded from the API and
UI.

### 6.3 Self-healing language

Use these precise labels:

- **Recovered:** deterministic replay or startup restoration completed.
- **Reconciled:** a future worker deterministically found and projected a
  durable event that had not yet received a projection receipt.
- **Degraded:** a durable event exists but its next deterministic step failed
  or is pending.
- **Unavailable:** a model adapter is not composed, refused, timed out, or
  returned invalid structured output.

Never use “self-healing” to mean an LLM invented a repair, suppressed history,
or altered a source-derived COP outside the projector.

## 7. Privacy and retention statement for this phase

Mosaic uses only checked-in synthetic data. No personal data, retention policy,
deletion workflow, or privacy classification is implemented or implied. This is
an explicit documentation boundary only; a real-data integration requires its
own privacy, retention, access, and audit design before implementation.

## 8. Implementation evidence

The P17–P20 increment satisfies the present-demo acceptance criteria:

- Public access works with no login, credential, or required header.
- Public actor/policy adapters are injected API seams; tests prove a deny
  policy can replace the public default without changing persistence or domain
  services.
- The operations endpoint and dashboard expose capability, status, bounded
  telemetry, source timestamps, lifecycle/model outcomes, and local stream
  facts without raw payload exposure.
- Startup/replay is labelled **recovered**. A nonzero unprojected count is
  labelled **degraded**, without claiming automatic reconciliation.
- SQLite remains the local default; no PostgreSQL container is added.
- The Docker runbook and E2E suite state that multi-instance support still
  requires durable dispatch, projection ownership, shared notification, and
  reconciliation design.
- The complete Go quality gate, Svelte check/build, and fresh isolated Docker
  smoke pass for the no-header dashboard, COP revision 9, and operations
  receipt.

## 9. Constraints that remain future work

Any future reconciliation must be durable, deterministic, idempotent, and
record a bounded outcome. A multi-instance deployment additionally requires a
PostgreSQL-backed store adapter, durable outbox/work handling, projector
ownership/lease semantics, and shared notification. Authentication, privacy,
retention, live model transport, and all operational actions remain out of
scope for this demo.
