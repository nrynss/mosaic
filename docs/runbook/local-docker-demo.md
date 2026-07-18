# Local Docker demo runbook

The image packages the dedicated `cmd/mosaicdemo` composition root. P12 keeps
the runtime server out of `tests/`; the deterministic in-process acceptance
check remains available at `go test ./tests/e2e -count=1`.

## Scope and prerequisites

This is one local, synthetic, single-instance demo. Docker Desktop (or an
equivalent Docker Engine with Compose v2) is required. No model, API key,
network model provider, `localmodels/` directory, or GGUF artifact is used by
the running container.

The image builds two deterministic artifacts:

- the P09 static dashboard, served same-origin with the P08 API; and
- the P07 `domestic-disturbance` fixture and its ontology schemas.

The image does not copy any other dataset material, local database, source
worktree, or model artifact. It runs as `nonroot` on port 8080 and writes only
to the mounted SQLite directory. The named `mosaic-data` volume survives a
normal stop; `mosaicdemo` runs the fixture idempotently on every start.

Compose first runs the short-lived `mosaic-data-init` service as root only to
create and assign the named volume to the runtime UID. It exits before
`mosaic` starts; the application container remains `nonroot`, read-only, and
without Linux capabilities.

## Start

From the repository root in PowerShell:

```powershell
docker compose up --build --detach
docker compose ps
```

Expected: service `mosaic` is running with `0.0.0.0:8080->8080/tcp`. Open
<http://localhost:8080>; the evidence-ledger dashboard should show synthetic
facts at COP state revision `9`.

If port 8080 is already in use, select a different host port for this PowerShell
session before starting:

```powershell
$env:MOSAIC_PORT = '8088'
docker compose up --build --detach
```

Use `http://localhost:8088` for the checks below. Remove the session setting
when finished with `Remove-Item Env:MOSAIC_PORT`.

## Verify the API and evidence boundary

```powershell
$port = if ($env:MOSAIC_PORT) { $env:MOSAIC_PORT } else { '8080' }
$base = "http://localhost:$port"
$viewer = @{ 'X-Mosaic-Demo-Identity' = 'viewer-demo' }
$supervisor = @{ 'X-Mosaic-Demo-Identity' = 'supervisor-demo' }

Invoke-WebRequest "$base/"
Invoke-RestMethod "$base/api/v1/health"
Invoke-RestMethod "$base/api/v1/version"
Invoke-RestMethod "$base/api/v1/cop" -Headers $viewer
Invoke-RestMethod "$base/api/v1/evidence/canonical_event/canonical-domestic-009-road-open" -Headers $viewer
```

Expected salient fields are dashboard HTTP `200`, `data.status: ok`,
`data.api_version: v1`, COP `data.state_revision: 9`, and evidence
`data.resolved: True`. The fixed viewer identity may only read. The fixed
supervisor identity may record immutable, non-operational audit history:

```powershell
$briefing = @{ briefing_id = 'briefing-local-demo'; note = 'Synthetic demo review.' } | ConvertTo-Json
Invoke-RestMethod "$base/api/v1/briefings" -Method Post -Headers $supervisor -ContentType 'application/json' -Body $briefing
```

Expected: HTTP `202` with `data.executed: False`. This records a briefing
request; it does not call Sol or take an operational action.

P08 accepts an audit action only for an already-persisted Insight or
Recommendation. The standard Docker startup intentionally does not synthesize
Terra/Sol output or invoke a model, so there is no such target in the fresh
container. The P12 acceptance test covers the advisory fixture adapters and a
successful `executed: false` audit action without an external model.

## Inspect the bounded SSE stream

`curl.exe` leaves the stream open by design. Stop it with `Ctrl+C` after the
first event:

```powershell
curl.exe --no-buffer -H "X-Mosaic-Demo-Identity: viewer-demo" "$base/api/v1/stream"
```

Expected first event:

```text
event: cop.snapshot
data: {"cop":...,"state_revision":9,...}
```

The automated test subscribes, waits for this snapshot, then publishes a
single local `system.status` notice. That ordering and its three-second context
deadline keep the SSE assertion deterministic instead of timing-sensitive.

## Stop and reset

```powershell
docker compose down
```

The named SQLite volume remains. To remove it and start the synthetic demo from
an empty durable store, run the following destructive reset:

```powershell
docker compose down --volumes
```

This deletes only Compose's `mosaic-data` volume; it does not affect
`localmodels/` or any repository files.

For startup diagnostics:

```powershell
docker compose logs --follow mosaic
```
