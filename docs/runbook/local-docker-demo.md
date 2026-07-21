# Local Docker demo runbook

The image packages the dedicated mosaicdemo composition root, including the
deterministic fixture-only Terra/Sol advisory replay. The in-process
acceptance check remains available with:

```powershell
go test ./tests/e2e -count=1
```

## Progressive board (default) vs seed-on-start

**Default:** the COP and advisories board start **empty**. Press **Play
scenario** (or `POST /api/v1/simulation/start`) to drive progressive
EventLog.Append + sync domain `ProcessBeat` until COP revision 9 and the
fixture Terra/Sol continuum land for the active simulation session.

| Env | Default | Effect |
|-----|---------|--------|
| `MOSAIC_SEED_ON_START` | off (`0`) | Progressive path: empty board until Play. Set `1` / `true` / `yes` / `on` for legacy bulk seed at boot (board at rev 9 immediately; ActiveSession isolation not wired). |
| `MOSAIC_SIM_BEAT_SPACING` | `2.5s` | Equal inter-beat SSE pacing on the progressive path. Use `1ms` in automated e2e. Go duration or integer milliseconds. |

**Reset vs durable store:** `POST /api/v1/simulation/reset` (and process
restart without volume wipe) does **not** truncate the append-only store.
Prior sessions' immutable records remain. Explicit **End** clears the active
session so GET `/cop` and `/advisories` return the empty-board policy; durable
history is still in Postgres/SQLite.

## Scope and prerequisites

This is one local, synthetic, single-instance demo. Docker Desktop (or an
equivalent Docker Engine with Compose v2) is required. The running container
uses no model, API key, network model provider, localmodels directory, or GGUF
artifact unless you inject a live OpenAI key (optional).

Access is intentionally open for this demo: there is no login, token, session,
or configured access restriction; the public actor/policy defaults permit these
calls. Do not treat X-Mosaic-Demo-Identity as a credential; it is optional
display metadata only and is not needed for any call below.

The image builds two deterministic artifacts:

- the public Svelte dashboard, bounded API, and fixture-only advisory history; and
- the domestic-disturbance fixture and its ontology schemas.

## Topology: two containers, one durable store

Compose defines a **two-service** topology:

| Service | Role | Durable? |
|---------|------|----------|
| `db` | PostgreSQL 16 (`postgres:16-alpine`) | Yes — named volume `postgres-data` |
| `mosaic` | Stateless app image (`cmd/mosaicdemo`) | No — `read_only` rootfs, `tmpfs` `/tmp` only |

```text
┌─────────────────┐     postgres://mosaic:mosaic@db:5432/mosaic
│ mosaic (app)    │ ──────────────────────────────────────────► │ db (Postgres) │
│ :8080           │                                             │ postgres-data │
└─────────────────┘                                             └───────────────┘
```

- `MOSAIC_DB_PATH` for the app service is a **Postgres DSN**, not a SQLite file:
  `postgres://mosaic:mosaic@db:5432/mosaic?sslmode=disable`
- Seed (domain scenario + fixture advisories), records, advisory history,
  operations telemetry, evidence resolution, and COP recovery all use **that
  single pgstore backend**. There is no parallel in-memory SQLite on the
  Compose path.
- The app image is intentionally **stateless**: no `/var/lib/mosaic` volume, no
  baked-in SQLite path. Durability is the Postgres volume alone.
- Before the app starts, Compose waits for `db` to pass `pg_isready`. The app
  container remains nonroot, read-only, and capability-dropped.

### Single-process local (SQLite) still works

Outside Compose, `mosaicdemo` defaults to a SQLite file under the process temp
directory when `MOSAIC_DB_PATH` is unset. Pass a `postgres://` or
`postgresql://` DSN to use Postgres instead. Do not mix: one process, one
backend.

This demo has no real data, privacy classification, retention workflow, or
deletion automation. Its checked-in records are synthetic only.

## Start

From the repository root in PowerShell:

```powershell
docker compose up --build --detach
docker compose ps
```

Expected: `db` is healthy; `mosaic` is running with `0.0.0.0:8080->8080/tcp`.
Open <http://localhost:8080>. **By default the board is empty** until you press
Play (or call `POST /api/v1/simulation/start`). After the progressive run
completes, COP state revision is 9 and fixture advisory cards appear for the
active session. The rev-7 assessment is labelled superseded and its
recommendation is labelled not current; neither is current operational advice.

To restore the legacy "seeded at boot" layout for a Compose session:

```powershell
$env:MOSAIC_SEED_ON_START = '1'
docker compose up --build --detach
```

If port 8080 is already in use, select a different host port for this
PowerShell session before starting:

```powershell
$env:MOSAIC_PORT = '8088'
docker compose up --build --detach
```

Use http://localhost:8088 for the checks below. Remove the session setting when
finished with `Remove-Item Env:MOSAIC_PORT`.

## Verify the public API and evidence boundary

No header is required:

```powershell
$port = if ($env:MOSAIC_PORT) { $env:MOSAIC_PORT } else { '8080' }
$base = "http://localhost:$port"

Invoke-WebRequest "$base/"
Invoke-RestMethod "$base/api/v1/health"
Invoke-RestMethod "$base/api/v1/version"
Invoke-RestMethod "$base/api/v1/cop"
Invoke-RestMethod "$base/api/v1/evidence/canonical_event/canonical-domestic-009-road-open"
Invoke-RestMethod "$base/api/v1/advisories"
Invoke-RestMethod "$base/api/v1/evidence/insight/insight-domestic-access-001"
Invoke-RestMethod "$base/api/v1/operations"
```

Expected salient fields are dashboard HTTP 200, health `data.status: ok`, and
version `data.api_version: v1`. On a **fresh progressive** boot (default), COP
`data.state_revision` is **0** and advisories are empty until Play. After
Play finishes (or with `MOSAIC_SEED_ON_START=1`), COP revision is **9**.
Evidence for a known fixture insight resolves with `data.resolved: True` once
that insight exists in the store. The advisory response is bounded: after the
fixture continuum lands it reports fixture-composed status, two superseded
Insights, and one not-current Recommendation. It does not return raw payload
bytes, checksums, prompts, model responses, or credentials.

The operations response is a bounded receipt, not a record export. On a fresh
progressive startup counts may be zero until Play; after seed or progressive
run it reports the recovered COP revision and fixture raw/canonical/
projection/lifecycle counts (read from Postgres). It labels Terra and Sol as
fixture-composed from local checked-in artifacts, not as live model transport;
durable reconciliation remains unavailable and external operational action
remains permanently unavailable. It does not expose raw payload bytes, raw
checksums, prompts, or model responses.

A public review request appends an immutable audit record and always reports
`executed: False`:

```powershell
$briefing = @{
  briefing_id = 'briefing-local-demo'
  note = 'Synthetic demo review.'
} | ConvertTo-Json
Invoke-RestMethod "$base/api/v1/briefings" -Method Post -ContentType 'application/json' -Body $briefing
```

This request does not invoke Sol or take an operational action. The
audit-actions route is public and now has a fixture Recommendation target; it
still appends only an immutable `executed: false` review record:

```powershell
$review = @{
  action = 'acknowledged'
  target_kind = 'recommendation'
  target_id = 'recommendation-domestic-001'
  note = 'Synthetic fixture review only.'
} | ConvertTo-Json
Invoke-RestMethod "$base/api/v1/audit-actions" -Method Post -ContentType 'application/json' -Body $review
```

## Verify interactive simulation and operator actions

This is the **default progressive path**. Start a new simulation session (Play):

```powershell
Invoke-RestMethod "$base/api/v1/simulation/start" -Method Post
```

Expected: Response status is HTTP 200, returning a `session_id` and the status `running`.
Beats are paced by `MOSAIC_SIM_BEAT_SPACING` (default 2.5s between beats).

Poll the status until the simulation naturally ends:

```powershell
Invoke-RestMethod "$base/api/v1/simulation/status"
```

Expected: Status field is `ended` and the `beats` array contains the replayed simulation beats.
After natural end the active session remains set so the final COP (revision 9)
and session-scoped advisories stay visible. Explicit `POST .../simulation/end`
clears the active session (empty board); it does **not** wipe the append-only store.

Perform an interactive Analyze operator request (Terra):

```powershell
$analyze = @{
  evidence = @(
    @{
      kind = 'raw_event'
      id = 'raw-domestic-001-call'
      explanation = 'Infrastructure incident reports matching weather alert context.'
    }
  )
  note = 'Analyze road closure reports.'
} | ConvertTo-Json -Depth 5
Invoke-RestMethod "$base/api/v1/operator/analyze" -Method Post -ContentType 'application/json' -Body $analyze
```

Expected: Returns status `refused` (under fixture mode) with `executed: false`, appending an audit record for the `public-demo` actor.

Prepare a Maintenance Handoff:

```powershell
$handoff = @{
  recipient = 'maintenance'
  target_kind = 'system'
  target_id = 'operator-maintenance-handoff'
  note = 'A prior road-condition handoff exists for area loc-road-brook-lane.'
} | ConvertTo-Json
Invoke-RestMethod "$base/api/v1/operator/prepare-handoff" -Method Post -ContentType 'application/json' -Body $handoff
```

Expected: Status HTTP 201, returning `executed: false`, `delivered: false`, and `handoff_status: "recorded"`. No external system is contacted.

Verify that `/api/v1/advisories` includes the newly recorded provenance trace and that no forbidden fields are leaked:

```powershell
$advisories = Invoke-RestMethod "$base/api/v1/advisories"
$advisories.audit_records | Format-Table audit_record_id, action, target_kind, target_id, note
```

Expected: The table includes the analyze and handoff audit records, showing they are correctly persisted. No prompts, model responses, raw bytes, or SHA256 fields are present.

Confirm that the operational projection (COP) is unchanged by the review actions:

```powershell
Invoke-RestMethod "$base/api/v1/cop"
```

Expected: `state_revision` is still 9. Bounded operator reviews do not mutate operational state.

## Verify retained-volume restart (Postgres durability)

Stop and restart **without** removing the named Postgres volume:

```powershell
docker compose down
docker compose up --detach
Invoke-RestMethod "$base/api/v1/advisories"
Invoke-RestMethod "$base/api/v1/operations"
Invoke-RestMethod "$base/api/v1/cop"
```

Expected after restart (progressive default):

- GET `/cop` and `/advisories` are **empty** until Play (ActiveSession is
  in-process and starts unset). Durable fixture/operator records remain in
  Postgres — confirm with the `psql` counts below.
- Re-Play is idempotent: P05 source identity and durable advisory stage
  classification skip already-intact artifacts (no duplicate fixture continuum).
- With `MOSAIC_SEED_ON_START=1`, COP `state_revision` is **9** again at boot
  (legacy bulk seed; ActiveSession isolation not wired).
- Public review audit records from the previous process remain immutable history.

Optional: confirm rows exist in Postgres directly:

```powershell
docker compose exec db psql -U mosaic -d mosaic -c "SELECT COUNT(*) AS canonical FROM canonical_events;"
docker compose exec db psql -U mosaic -d mosaic -c "SELECT COUNT(*) AS insights FROM insights;"
```

## Inspect the public bounded SSE stream

curl.exe leaves the stream open by design. Stop it with Ctrl+C after the first
event:

```powershell
curl.exe --no-buffer "$base/api/v1/stream"
```

Expected first event (after Play has projected state, or with seed-on-start):

```text
event: cop.snapshot
data: {"cop":...,"state_revision":9,...}
```

On a fresh progressive boot before Play, the snapshot may report revision 0 /
empty COP.

The HTTP stream broker is process-local and best-effort. Cross-instance fan-out
is the event-spine `EventBus` (Postgres `LISTEN/NOTIFY` today); see the
Kafka/Redpanda introduction guide for the pluggable transport story. This
Compose layout is still single-instance for the app container.

## Current capability boundary

- Interactive simulation replay is controlled via start/reset/end routes, broadcasting ordered beats over the session-scoped stream.
- Deterministic checkpoint/replay recovery is composed; on Postgres the API prefers the materialized COP read model when available.
- There is no durable reconciliation worker, autonomous recovery process, or shared projection ownership/lease beyond the Postgres consumer-group design in the event spine.
- Terra, Sol, and Luna default and fall back to deterministic local checked-in fixtures when no live provider key is configured.
- Live models can be optionally configured at startup via environment variables using a server-side OpenAI API key, which is never exposed to the client.
- Mosaic never dispatches, contacts, or mutates an external operational system.
- **Durable store in Compose is PostgreSQL.** Local non-Docker runs may still use SQLite via a file path for `MOSAIC_DB_PATH`.

## Stop and reset

```powershell
docker compose down
```

The named Postgres volume remains. To remove it and start the synthetic demo from
an empty durable store, run this destructive reset only when that is intended:

```powershell
docker compose down --volumes
```

This removes only Compose's `postgres-data` volume. It does not affect localmodels
or repository files.

For startup diagnostics:

```powershell
docker compose logs --follow mosaic
docker compose logs --follow db
```

## Fresh Docker smoke

For an isolated release-style smoke, remove only this demo's named volume,
then build, start, and run the public no-header checks above:

```powershell
docker compose down --volumes
docker compose up --build --detach
Invoke-RestMethod "$base/api/v1/advisories"
docker compose down
```
