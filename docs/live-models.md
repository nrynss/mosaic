# Interactive Demo — Live OpenAI Model Integration

This document outlines the design, configuration, and constraints for integrating live AI model execution (Luna, Terra, and Sol) in the interactive operator demo.

## Overview & Default Path

By default, the demo runs in a fully deterministic, network-free **fixture mode**. All interactive operator analysis and briefings use pre-packaged local scenarios. 

Live models are strictly **opt-in** and require a server-only OpenAI API key.

## Server-Only Key Safety

To prevent accidental key exposure:
* **No Client Leakage**: The OpenAI API key is read exclusively by the server process from the environment at startup. It is never sent to the browser, stored in client-side cookies/local storage, or written to public-facing telemetry.
* **No Repo Secrets**: The API key must never be committed to Git, hardcoded in configuration files, or built into Docker images.
* **No Query Flags**: The key is never configured via CLI flags or URL query parameters to avoid process-list inspection leaks.

## Configuration & Provider Selection

Provider selection is configured at startup using process environment variables. If a provider is not specified or is set to an unknown value, it defaults to `fixture`.

| Variable | Description | Allowed Values | Default |
|---|---|---|---|
| `OPENAI_API_KEY` | Server-only OpenAI API key | *Secret String* | *None* |
| `MOSAIC_LUNA_PROVIDER` | Ingestion & normalisation mode for Luna | `fixture`, `live` | `fixture` |
| `MOSAIC_TERRA_PROVIDER` | Interactive incident assessment mode for Terra | `fixture`, `live` | `fixture` |
| `MOSAIC_SOL_PROVIDER` | Interactive recipient briefing mode for Sol | `fixture`, `live` | `fixture` |

### Fixture Fallback

If an agent's provider is configured as `live` but the `OPENAI_API_KEY` is absent from the server environment, the demo automatically falls back to `fixture` mode for that agent. This fallback status is reported in the capabilities metadata returned by the `/api/v1/operations` endpoint.

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
* **Transport Failure / Timeout**: If the OpenAI service is unreachable or times out, the system logs a `ModelRun` with `validation_status: "failed"` or `"timed_out"` containing the failure details, and the operator request is gracefully declined.

## Local Docker Setup with Live Models

To opt into live models in your local Docker environment, define the required variables. You can pass them inline or use a local `.env` file at the repository root (which is gitignored by default):

~~~bash
# Example: Running the interactive demo with a live Terra model
$env:OPENAI_API_KEY="your-api-key-here"
$env:MOSAIC_TERRA_PROVIDER="live"
docker compose up --build --detach
~~~
