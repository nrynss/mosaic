# Interactive Demo — Live OpenAI Model Integration

This document outlines the design, configuration, and constraints for integrating live AI model execution (Luna, Terra, and Sol) in the interactive operator demo.

## Overview

The process can run each agent in **`fixture`** or **`live`** mode.

* **Process default (no env):** `fixture` — deterministic, network-free, historical advisory cards.
* **Docker Compose default:** `live` for Luna, Terra, and Sol, with `OPENAI_API_KEY` taken from the root `.env`. Without a key, the process still falls back to fixture.
* **Cloud Run:** set `MOSAIC_*_PROVIDER=live` plus the server-only key (Secret Manager preferred).

Live mode means real OpenAI clients are composed. **Billing balance is not a mode gate.** A present key + `live` keeps the UI on live even if OpenAI later returns insufficient-quota errors; those are recorded as failed model runs.

## Server-Only Key Safety

To prevent accidental key exposure:
* **No Client Leakage**: The OpenAI API key is read exclusively by the server process from the environment at startup. It is never sent to the browser, stored in client-side cookies/local storage, or written to public-facing telemetry.
* **No Repo Secrets**: The API key must never be committed to Git, hardcoded in configuration files, or built into Docker images.
* **No Query Flags**: The key is never configured via CLI flags or URL query parameters to avoid process-list inspection leaks.

## Configuration & Provider Selection

Provider selection is configured at startup using process environment variables.

| Variable | Description | Allowed Values | Process default (unset) | Compose default |
|---|---|---|---|---|
| `OPENAI_API_KEY` | Server-only OpenAI API key | *Secret String* | *None* | From root `.env` |
| `MOSAIC_LUNA_PROVIDER` | Ingestion & normalisation mode for Luna | `fixture`, `live` | `fixture` | `live` |
| `MOSAIC_TERRA_PROVIDER` | Interactive incident assessment mode for Terra | `fixture`, `live` | `fixture` | `live` |
| `MOSAIC_SOL_PROVIDER` | Interactive recipient briefing mode for Sol | `fixture`, `live` | `fixture` | `live` |
| `MOSAIC_SIM_MODE` | Simulation inference cassette mode (Terra/Sol) | `fixture` / `passthrough` / `off`, `live` / `record`, `replay` / `recorded` | `fixture` (passthrough) | `fixture` |
| `MOSAIC_CASSETTE_MODE` | Alias of `MOSAIC_SIM_MODE` (ignored when SIM is set) | same as above | unset | unset |
| `MOSAIC_CASSETTE_DIR` | Directory for cassette FileStore recordings | path | `$TMPDIR/mosaic-recordings` (writable under read-only containers) | `/tmp/mosaic-recordings` |

### Simulation cassette modes (Live / Replay / Fixture)

Terra and Sol structured clients can be wrapped with a **cassette** decorator
(`internal/simulation/cassette`) so one paid live run can be banked and replayed
without further API cost. Luna is not cassette-wrapped (ingestion path is
independent).

| Mode (`MOSAIC_SIM_MODE`) | Cassette | Inner client | API cost |
|---|---|---|---|
| **fixture** (default / CI) | Passthrough (no decorator) | refuse / fixture clients | None |
| **live** / **record** | **Record** | real OpenAI when key + `MOSAIC_*_PROVIDER=live` | Yes |
| **replay** / **recorded** | **Replay** | unused (`nil`); store only | None |

Rules:

1. **Default is fixture/passthrough** — safe for CI and Docker without keys.
2. **Record** wraps only agents that are effectively live (provider `live` **and** key present). If mode is `live`/`record` but the key is missing or providers stay fixture, composition demotes to passthrough (same as existing provider fallback; refusals are not banked).
3. **Replay** does **not** require `OPENAI_API_KEY`. Terra/Sol always use the FileStore; a missing recording returns `cassette.ErrReplayMiss` (never a silent network call).
4. Per-agent `MOSAIC_TERRA_PROVIDER` / `MOSAIC_SOL_PROVIDER` still gate which agents are live when mode is live/record.
5. Cassette mode is **process-level env only** (no UI secrets, no mid-process hot-swap). The UI surfaces `cassette_mode` from `/api/v1/version` and `/api/v1/advisories` and shows a mode pill (Fixture / Live recording / Replay).
6. **“Refresh banked advice”** (UI) is enabled only when the process was started with `MOSAIC_SIM_MODE=replay`. It re-fetches advisories from the server; it does **not** re-bank a live run or change mode. True free Terra/Sol cassette use requires the process already in replay mode when those clients are invoked.

Example — bank one run, then replay:

```powershell
# Bank (paid): live providers + record mode
$env:OPENAI_API_KEY = "sk-..."
$env:MOSAIC_TERRA_PROVIDER = "live"
$env:MOSAIC_SOL_PROVIDER = "live"
$env:MOSAIC_SIM_MODE = "live"
$env:MOSAIC_CASSETTE_DIR = Join-Path $env:TEMP "mosaic-recordings"
# run mosaicdemo once through the advisory beats...

# Replay (free): no key required — same CASSETTE_DIR as the banked run
Remove-Item Env:OPENAI_API_KEY -ErrorAction SilentlyContinue
$env:MOSAIC_SIM_MODE = "replay"
$env:MOSAIC_CASSETTE_DIR = Join-Path $env:TEMP "mosaic-recordings"
```

### Effective selection rules

1. Agent is **`live`** only when the env requests `live` **and** `OPENAI_API_KEY` is non-empty (and sim mode is not forcing Terra/Sol onto replay).
2. If env requests `live` but the key is **absent**, that agent **falls back to `fixture`**.
3. If env requests `live`, key is present, but OpenAI returns billing/quota/network errors → agent stays **`live`** in capability metadata; the invocation is recorded as failed/refused/timed_out.
4. `MOSAIC_SIM_MODE=replay` forces Terra/Sol onto the cassette store regardless of provider flags; reported provider selection for those agents is fixture.

This fallback status is reported in the `providers` object on advisories / operator responses (what the UI badges show).

## Capability Boundary & Safety Rules

### 1. Models Inform; They Never Mutate
Live model outputs are strictly advisory. They exist to inform the operator, who remains the final authority:
* **COP Immutability**: No live model response can ever mutate the deterministic operational projection (COP).
* **Operator Reviews**: Operator decisions (Analyze, Approve, Annotate, Prepare Handoff) are captured as immutable audit records with `executed: false`. They are never sent to external actors.
* **No LLM Self-Healing**: The system does not support autonomous loops, self-correction, or automated outbox dispatching based on model predictions.

### 2. Failure & Refusal Logging
All model invocations (successful, refused, or failed) are recorded in the database as immutable `ModelRun` records to preserve a complete audit trail:
* **Successful Response**: Creates a `ModelRun` with `validation_status: "valid"` and stores the generated insight/recommendation.
* **Model Refusal**: If the OpenAI client returns an API-level refusal, the system logs a `ModelRun` with `validation_status: "refused"` containing the refusal details, and generates no operational artifacts.
* **Transport Failure / Timeout / Quota**: If the OpenAI service is unreachable, times out, or rejects for billing/quota, the system logs a `ModelRun` with `validation_status: "failed"` or `"timed_out"` containing the failure details, and the operator request is gracefully declined.

## Local Docker Setup with Live Models

Root `.env` (gitignored):

~~~bash
OPENAI_API_KEY=sk-proj-your-openai-api-key-here
~~~

Compose injects the key and defaults all three providers to `live`:

~~~bash
docker compose up --build --detach
~~~

Override a single agent back to fixture if needed:

~~~bash
# PowerShell session
$env:MOSAIC_LUNA_PROVIDER = "fixture"
docker compose up --build --detach
~~~

## Cloud Run

Ensure the service has both the key and the provider flags (key alone is not enough):

~~~bash
gcloud run services update mosaic-demo \
  --region=us-central1 \
  --update-env-vars="MOSAIC_LUNA_PROVIDER=live,MOSAIC_TERRA_PROVIDER=live,MOSAIC_SOL_PROVIDER=live"
~~~

Prefer `--set-secrets=OPENAI_API_KEY=openai-api-key:latest` for the key. After the revision rolls out, UI badges should show `live` for each agent.

## Estimated-Credits Meter

OpenAI does not expose a balance or spend-remaining endpoint for project API keys, so Mosaic cannot ask the provider "how much credit is left." Instead the server keeps an honest, local **estimate**:

* Every successful live Luna/Terra/Sol call reports its `usage.input_tokens` / `usage.output_tokens` from the OpenAI Responses API response.
* An in-memory, mutex-guarded accumulator (`internal/usage`) multiplies those counts by a **hardcoded per-model price table** and tallies a running total.
* `GET /api/v1/model-usage` returns `estimated_spend_usd`, `input_tokens`, `output_tokens`, `live_runs`, `since`, and a `note` disclaimer. The UI's developer console and a small chip next to the agent badges surface this.

Optional configuration:

| Variable | Description | Allowed Values | Default |
|---|---|---|---|
| `MOSAIC_DEMO_BUDGET_USD` | Optional demo budget used to compute `estimated_remaining_usd` | Any parseable float | Unset (budget fields omitted) |

Limitations (by design, not oversights):
* **Hardcoded prices.** The price table lives in `internal/usage/usage.go` and is not fetched from a live pricing feed; if OpenAI's prices change, or Mosaic starts requesting a different model, the table needs a manual update.
* **Per-process only.** The accumulator is in-memory and is never written to SQLite — Cloud Run's `/tmp` is ephemeral anyway, so a per-process estimate is the honest scope. It resets to zero whenever the process restarts.
* **Only counts Mosaic's own calls.** It has no visibility into any other usage on the same OpenAI API key (other tools, other deployments, dashboard usage outside this process).
* **Not a real balance.** It is never a substitute for checking the actual usage/billing dashboard at platform.openai.com.
