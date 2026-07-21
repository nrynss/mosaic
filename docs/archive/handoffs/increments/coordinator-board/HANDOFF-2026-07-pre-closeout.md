# Mosaic Handoff Board

This is the central coordinator surface for the Mosaic project. The greenfield interactive operator demo increment (v0.3) is fully integrated, verified, and complete.

## Verification of the Interactive Operator Demo (v0.3)

All v0.3 parcels (P35–P47) have been merged and integrated into `main`. The implementation is fully verified across the entire stack:

1. **Simulation Control**: Starts, pauses, and ends synthetic session beat emission in configured scenario timing.
2. **Operator API & Key Safety**: Supports generative Analyze (Terra) and briefing (Sol) operator reviews under `executed: false` safety. Key is server-only.
3. **Department Handoffs**: Captures noted intent to recipient mailboxes (Dispatch/Maintenance) as `executed: false` and `delivered: false` audits without external side effects.
4. **Interactive UI Workspace**: Refactored to feature tabs for the incident timeline command panel, live elapsed counters, recurrence alerts, and detailed audit trails.
5. **Local / Docker / Cloud Run Persistence**: v0.4 Compose and Cloud Run use
   **Postgres**. Local and hosted demos share **Supabase** (`openai-devdays`)
   when `MOSAIC_DB_PATH` is the session-pooler DSN. SQLite remains available for
   zero-infra unit/e2e tests only.

### Verification Status & Quality Gate

* **End-to-End Tests**: `go test ./tests/e2e -count=1` runs the full E2E interactive loop and passes successfully.
* **Go Quality Gate**: `go run ./cmd/mosaic quality` passes all format, vet, static, and package checks.
* **Svelte Quality Gate**: `npm run check` and `npm run build` compile cleanly without warnings or errors.
* **Docker Verification**: The application builds and runs successfully in Docker using Compose. All public routes, streams, and operator actions have been smoke tested.

---

## Historical Handoff Logs

The detailed parcel breakdowns and logs for completed increments are preserved in the archive:

* **[v0.1 Foundation (Archived)](docs/archive/HANDOFF-v0.1-foundation.md)**: Green-field architecture, ingestion spine, deterministic projector, and SQLite storage.
* **[v0.2 Fixture Advisory (Archived)](docs/archive/HANDOFF-v0.2-fixture-advisory.md)**: Frozen scenario replay, public bounded API, and static evidence resolution dashboard.
* **[v0.3 Interactive Operator Demo (Archived)](docs/archive/HANDOFF-v0.3-interactive-operator-demo.md)**: Interactive simulation, opt-in live models, recurrence alerts, and operator review audit logging.

---

## Cloud Run + Supabase (durable demo)

The live service is **up now** on the chosen durable topology: **Cloud Run +
Supabase Postgres** (project `openai-devdays`). Local Docker Compose uses the
**same** Supabase database via root `.env` `MOSAIC_DB_PATH`.

* **Public URL**: **[https://mosaic.nryn.dev](https://mosaic.nryn.dev)** (Cloudflare → Cloud Run)
* **Cloud Run URL**: [https://mosaic-demo-358513274447.us-central1.run.app](https://mosaic-demo-358513274447.us-central1.run.app)
* **Live now**: single Cloud Run service, `--max-instances=1`, `--concurrency=8`.
* **Durable store**: `MOSAIC_DB_PATH` from Secret Manager secret `mosaic-db-dsn`
  (Supabase **session** pooler, port 5432). App migrations run on boot.
* **Local parity**: set the same pooler DSN in gitignored `.env`; Compose
  interpolates it so local and Cloud Run share one board/history.
* **Still process-local**: SSE/sim streams keep `--max-instances=1`. Cassette
  banks under `/tmp` remain ephemeral.
* **Runbook**:
  **[Cloud Run Deployment Analysis](docs/runbook/cloud-run-deployment-analysis.md)**
  §6 (session pooler + secret wiring).

---

## Planned: Playwright demo capture (not built yet)

After the UI freezes for the submission recording, we plan a small standalone
tool (e.g. `tools/demo-recorder/`) using **Playwright** to produce a repeatable
demo video of the interactive walkthrough.

**Intent**

* Drive the **same steps** as [`docs/demo-script.md`](docs/demo-script.md)
  (connection → Play scenario → COP → advice → handoffs → Decision history).
* Sync advancement to **simulation SSE beats** (`/api/v1/simulation/stream`)
  and UI-ready selectors, plus explicit **hold times** for voiceover (scenario
  `delay_ms` is ~100ms — beat-only pacing is too fast for a narrated take).
* Record at **1920×1080** with Playwright’s built-in video (`.webm`); post with
  **system ffmpeg** → H.264 `.mp4` (trim / optional mux of narration).
* Optional later: in-page CSS zoom, cursor overlay, step captions — not required
  for a first pass.

**When to build**

* **Not while the UI is still thrashing.** Prefer after a short freeze window
  before the final recording.
* First takes against **local Docker** (cheap retries); optional clean take
  against **https://mosaic.nryn.dev** (or the Cloud Run URL).

**Out of scope for v1**

* Full cinematic Ken Burns pipeline as a day-one requirement.
* Replacing the presenter script — the script stays the source of truth; the
  recorder executes it.

---

## Planned: v0.4 — Pluggable Event Spine, Durable Persistence, Simulation Isolation

The next increment makes the interactive path **honestly event-driven**: the
simulation drives the real pipeline beat-by-beat (real progressive reveal and
real-time inference), persistence moves to **PostgreSQL** as the single
operational dependency (log + `SKIP LOCKED` queue + `LISTEN/NOTIFY` fan-out +
materialized COP), and the log is designed behind interfaces so **Kafka/Redpanda
plugs in later** with no producer/consumer changes. Simulation is isolated into
its own package with three modes — **Live / Replay (recorded, no API) / Fixture**.

* **Design + task parcels (T-shirt sized):** [docs/HANDOFF-v0.4-pluggable-event-spine.md](docs/HANDOFF-v0.4-pluggable-event-spine.md)
* **Fallback:** if the spine does not land, retreat to cosmetic UI/simulation-only
  changes (beat pacing + Replay button on the seeded model). `main` always keeps a
  working demo.

---

## Next Steps

> [!NOTE]
> **Durable demo path is live**: Cloud Run + Supabase (session pooler) with
> shared local Compose DSN. Budget alerts on GCP still recommended.

The actual project next steps are:
* **End-to-End Run with Paid API Key**: Execute a full live model test run (with active credits) to verify generative Terra, Sol, and Luna responses and outputs.
* **UI Refinement & Polish**: Final visual pass; freeze labels/selectors before any automated capture.
* **Demo Preparation**: Walkthrough + end VO are in [`docs/demo-script.md`](docs/demo-script.md); **YouTube &lt;3 min plan** (what we show, Codex + GPT-5.6 VO, beat map) is in [`docs/demo-video.md`](docs/demo-video.md). Record when UI is stable (manual or planned Playwright tool above).
* **Playwright demo-recorder** (planned): Beat/SSE-aware automated capture — see section above; build only after UI freeze.
* **Project Details Page**: Author and publish a dedicated project description page on the **nryn.dev** site detailing the architecture, constraints, and results.
