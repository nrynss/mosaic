# Mosaic Handoff Board

Live coordinator surface for the current demo posture. Historical increment
boards live under
[`docs/archive/handoffs/`](docs/archive/handoffs/README.md).

## Current status (2026-07)

**v0.4 pluggable event spine is integrated on `main`.** Progressive simulation,
Postgres event spine, live OpenAI Luna/Terra/Sol, demo cassettes, Playwright
E2E, and durable **Cloud Run + Supabase** are the production demo path.

| Area | State |
|------|--------|
| Progressive Play | Empty board until Play; real EventLog → ProcessBeat → COP rev 9 |
| Store | Postgres (`pgstore`); Compose default local Postgres or shared Supabase DSN |
| Cloud Run | Single instance; `MOSAIC_DB_PATH` from Secret Manager `mosaic-db-dsn` (session pooler) |
| Models | Providers `live` when key present; cassette `live`/`record` or `replay` |
| Demo interactions | Packaged `testdata/demo/recording-manifest.json` in image |
| Playwright | `ui/e2e` suite + walkthrough record path (see runbook) |

### Live surfaces

* **Public:** [https://mosaic.nryn.dev](https://mosaic.nryn.dev)
* **Cloud Run:** [https://mosaic-demo-358513274447.us-central1.run.app](https://mosaic-demo-358513274447.us-central1.run.app)
* **Local:** `docker compose up --build` → http://localhost:8080

### Spend honesty

| Path | OpenAI cost |
|------|-------------|
| **Play scenario** | No — synthetic beats, real pipeline, fixture advisory continuum |
| **Model Actions** (Luna Interpret / Terra Analyze / Sol Brief) | Yes when `MOSAIC_*_PROVIDER=live` + `OPENAI_API_KEY` |
| **Replay** (`MOSAIC_SIM_MODE=replay`) | No — banked cassette responses |

### Quality gates (expected green)

* `go run ./cmd/mosaic quality`
* `go test ./tests/e2e -count=1`
* `npm run check` / `npm run build` under `ui/`
* Docker Compose smoke of public routes

## Next steps

* Clean live walkthrough: Play → rev 9 → Terra/Sol **valid** (not invalid schema)
* Optional: project page on nryn.dev; GCP budget alerts
* Supabase free-tier: projects pause after 7 idle days — un-pause before demo day

## Archived handoffs

Full parcel boards and design notes (do not edit as live status):

| Increment | Archive |
|-----------|---------|
| v0.1 Foundation | [docs/archive/handoffs/increments/v0.1-foundation/HANDOFF.md](docs/archive/handoffs/increments/v0.1-foundation/HANDOFF.md) |
| v0.2 Fixture advisory | [docs/archive/handoffs/increments/v0.2-fixture-advisory/HANDOFF.md](docs/archive/handoffs/increments/v0.2-fixture-advisory/HANDOFF.md) |
| v0.3 Interactive operator | [docs/archive/handoffs/increments/v0.3-interactive-operator-demo/HANDOFF.md](docs/archive/handoffs/increments/v0.3-interactive-operator-demo/HANDOFF.md) |
| v0.4 Event spine | [docs/archive/handoffs/increments/v0.4-pluggable-event-spine/HANDOFF.md](docs/archive/handoffs/increments/v0.4-pluggable-event-spine/HANDOFF.md) |
| Index | [docs/archive/handoffs/README.md](docs/archive/handoffs/README.md) |

## Further reading

| Doc | Purpose |
|-----|---------|
| [`README.md`](README.md) | Project overview and deploy |
| [`docs/live-models.md`](docs/live-models.md) | Fixture vs live vs cassette |
| [`docs/runbook/local-docker-demo.md`](docs/runbook/local-docker-demo.md) | Local Compose verification |
| [`docs/runbook/cloud-run-deployment-analysis.md`](docs/runbook/cloud-run-deployment-analysis.md) | Cloud Run + Supabase §6 |
| [`docs/runbook/playwright-demo-e2e.md`](docs/runbook/playwright-demo-e2e.md) | Playwright demo E2E |
