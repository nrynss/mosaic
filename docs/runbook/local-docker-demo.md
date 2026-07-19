# Local Docker demo runbook

The image packages the dedicated mosaicdemo composition root, including the
deterministic fixture-only Terra/Sol advisory replay. The in-process
acceptance check remains available with:

~~~powershell
go test ./tests/e2e -count=1
~~~

## Scope and prerequisites

This is one local, synthetic, single-instance demo. Docker Desktop (or an
equivalent Docker Engine with Compose v2) is required. The running container
uses no model, API key, network model provider, localmodels directory, or GGUF
artifact.

Access is intentionally open for this demo: there is no login, token, session,
or configured access restriction; the public actor/policy defaults permit these
calls. Do not treat X-Mosaic-Demo-Identity as a credential; it is optional
display metadata only and is not needed for any call below.

The image builds two deterministic artifacts:

- the public Svelte dashboard, bounded API, and fixture-only advisory history; and
- the domestic-disturbance fixture and its ontology schemas.

The container contains no PostgreSQL service. It runs a single process with one
SQLite file on the named mosaic-data volume. The database volume survives a
normal stop, and mosaicdemo idempotently re-delivers the frozen fixture on
startup.

Compose first runs the short-lived mosaic-data-init service as root only to
create and assign the named volume to the runtime UID. It exits before mosaic
starts; the application container remains nonroot, read-only, and without Linux
capabilities.

This demo has no real data, privacy classification, retention workflow, or
deletion automation. Its checked-in records are synthetic only.

## Start

From the repository root in PowerShell:

~~~powershell
docker compose up --build --detach
docker compose ps
~~~

Expected: mosaic is running with 0.0.0.0:8080->8080/tcp. Open
<http://localhost:8080>; the public evidence ledger and its operations receipt
should show synthetic facts at COP state revision 9 and historical advisory
cards. The rev-7 assessment is labelled superseded and its recommendation is
labelled not current; neither is current operational advice.

If port 8080 is already in use, select a different host port for this
PowerShell session before starting:

~~~powershell
$env:MOSAIC_PORT = '8088'
docker compose up --build --detach
~~~

Use http://localhost:8088 for the checks below. Remove the session setting when
finished with Remove-Item Env:MOSAIC_PORT.

## Verify the public API and evidence boundary

No header is required:

~~~powershell
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
~~~

Expected salient fields are dashboard HTTP 200, health data.status: ok, version
data.api_version: v1, COP data.state_revision: 9, and evidence data.resolved:
True. The advisory response is bounded: it reports fixture-composed status,
two superseded Insights, and one not-current Recommendation. It does not
return raw payload bytes, checksums, prompts, model responses, or credentials.

The operations response is a bounded receipt, not a record export. On a fresh
startup it reports the recovered COP revision and fixture raw/canonical/
projection/lifecycle counts. It labels Terra and Sol as fixture-composed from
local checked-in artifacts, not as live model transport; durable
reconciliation remains unavailable and external operational action remains
permanently unavailable. It does not expose raw payload bytes, raw checksums,
prompts, or model responses.

A public review request appends an immutable audit record and always reports
executed: False:

~~~powershell
$briefing = @{
  briefing_id = 'briefing-local-demo'
  note = 'Synthetic demo review.'
} | ConvertTo-Json
Invoke-RestMethod "$base/api/v1/briefings" -Method Post -ContentType 'application/json' -Body $briefing
~~~

This request does not invoke Sol or take an operational action. The
audit-actions route is public and now has a fixture Recommendation target; it
still appends only an immutable `executed: false` review record:

~~~powershell
$review = @{
  action = 'acknowledged'
  target_kind = 'recommendation'
  target_id = 'recommendation-domestic-001'
  note = 'Synthetic fixture review only.'
} | ConvertTo-Json
Invoke-RestMethod "$base/api/v1/audit-actions" -Method Post -ContentType 'application/json' -Body $review
~~~

## Verify retained-volume restart

Stop and restart without removing the named volume:

~~~powershell
docker compose down
docker compose up --detach
Invoke-RestMethod "$base/api/v1/advisories"
Invoke-RestMethod "$base/api/v1/operations"
~~~

The same fixture Insight, Recommendation, Model Run, and fixture Audit Record
identities are reused; restart appends no duplicate fixture advisory history.
The public review record above remains immutable history and does not change
the recovered COP revision.

## Inspect the public bounded SSE stream

curl.exe leaves the stream open by design. Stop it with Ctrl+C after the first
event:

~~~powershell
curl.exe --no-buffer "$base/api/v1/stream"
~~~

Expected first event:

~~~text
event: cop.snapshot
data: {"cop":...,"state_revision":9,...}
~~~

The broker is process-local and best-effort. It is not shared notification and
does not make this container multi-instance capable. The automated test
subscribes, waits for the snapshot, then publishes one local system.status
notice with a three-second context deadline.

## Current capability boundary

- Deterministic checkpoint/replay recovery is composed and reported as
  recovered for the current observation.
- There is no durable reconciliation worker, autonomous recovery process, or
  shared projection ownership/lease.
- Terra and Sol are composed only through local checked-in structured fixtures;
  there is no live model transport, credential, network model request, or GGUF.
- Mosaic never dispatches, contacts, or mutates an external operational system.

PostgreSQL, shared dispatch/outbox, and multi-instance coordination are future
design work; they are not included by this Docker demo.

## Stop and reset

~~~powershell
docker compose down
~~~

The named SQLite volume remains. To remove it and start the synthetic demo from
an empty durable store, run this destructive reset only when that is intended:

~~~powershell
docker compose down --volumes
~~~

This removes only Compose's mosaic-data volume. It does not affect localmodels
or repository files.

For startup diagnostics:

~~~powershell
docker compose logs --follow mosaic
~~~

## Fresh Docker smoke

For an isolated release-style smoke, remove only this demo's named volume,
then build, start, and run the public no-header checks above:

~~~powershell
docker compose down --volumes
docker compose up --build --detach
Invoke-RestMethod "$base/api/v1/advisories"
docker compose down
~~~
