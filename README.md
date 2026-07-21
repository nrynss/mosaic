# Mosaic

**Safe AI next to systems that must stay true.**

Mosaic is a **developer framework** for putting generative agents beside a
**deterministic, auditable core** — streaming state, human gates, and immutable
provenance — without letting models rewrite operational truth.

This repository ships both:

1. **The framework contracts** — layers, APIs, agent seams, audit rules.
2. **A running reference implementation** — synthetic high-stakes incident
   flow + CAD-style operator UI so you can *see* the density of real events
   through those contracts.

**Live demo:** [https://mosaic.nryn.dev](https://mosaic.nryn.dev)
([Cloud Run](https://mosaic-demo-358513274447.us-central1.run.app))

In-app **Help** and **?** tips walk operators through the board.

---

## Origin

Built solo, in four days, on a simple belief: AI should supplement people, not
replace the person in the chair. A senior leader once called AI the new TCP. I
think that is right, and I wanted something that treats it that way. AI proposes.
A human still decides. Everything else here follows from that.

The reference domain is emergency operations because the cost of getting it wrong
is one you can feel. The framework itself does not care about the domain. The pain
of getting it wrong is universal.

---

## Why it exists

Enterprises and governments want AI in the loop — not AI in a black box. They
need to know:

- **where** models touch their data;
- **how** advice interfaces with systems of record that must stay deterministic;
- **how to audit** every recommendation after the fact (including advice that
  was later superseded).

Mosaic demonstrates those boundaries in code: agents **propose**, a
deterministic **projector** alone **disposes** the common operating picture
(COP), and every model run and human decision is an immutable record
(`executed: false` for handoffs in this demo — never silent external action).

---

## Framework + reference implementation

| | |
|--|--|
| **Framework** | Layered architecture, versioned schemas, public HTTP + SSE API, pluggable agents, immutable audit trail, domain-profile seam, event-log interfaces |
| **This implementation** | Domestic-disturbance synthetic profile, **Postgres** spine (`pgstore`; SQLite for zero-infra tests), OpenAI-backed Luna/Terra/Sol (or fixture/replay), Svelte reference UI |

The UI is a **CAD-style reference client**, not the product boundary. Another
client (mobile field app, SOC console, EOC wallboard) can consume the same
streams and APIs. Judge the **contracts**; the board proves they work under a
dense event flow.

### Current architecture (v0.4)

| Piece | Behaviour |
|-------|-----------|
| **Progressive simulation** | Board starts **empty**. **Play scenario** appends each beat to the event log and runs the real ingest → project path (COP advances 1→9). |
| **Event spine** | `EventLog` / `EventConsumer` / `EventBus` interfaces; Postgres implements append, ordered consume, and `LISTEN/NOTIFY` fan-out. Kafka/Redpanda is a later transport swap. |
| **Agents** | Luna / Terra / Sol: fixture, live OpenAI, or cassette **replay** (banked live output, $0). |
| **Spend boundary** | **Play does not call OpenAI.** Live spend is **Model Actions** (Interpret / Analyze / Brief) when providers are `live` and a server-only key is set. |

### Four layers

| Layer | Role |
|-------|------|
| **1. Deterministic core** | Ingestion → canonical events → **projector** → **COP** + immutable store. Only the projector mutates operational state. |
| **2. Agent layer** | **Luna / Terra / Sol** — generative advice, fixture / live / replay, swappable without rewriting the core. |
| **3. Transport** | Bounded REST + **SSE** (`/api/v1/stream`, `/api/v1/simulation/stream`). |
| **4. Presentation** | Reference UI (or any consumer of the API). |

### Three guarantees to remember

1. **Mutability** — Models cannot write the COP. They only propose.  
2. **Provenance** — You can explain *why* advice existed at state revision *N*
   after revision *N+1* moved on.  
3. **Human gate** — Operator reviews and handoffs are recorded intent; this demo
   never claims external delivery.

---

## Boost OpenAI with Luna, Terra, and Sol

Mosaic is built to **use OpenAI well in production-shaped systems**: not one
monolithic “do everything” prompt, but **specialized agent roles** with clear
jobs, schemas, and audit trails.

| Agent | Class of work | Optimization angle |
|-------|----------------|--------------------|
| **Luna** | Event interpretation / normalisation (lightweight, high volume) | Prefer fast, cheaper models for ingest-path volume; keep structure strict and validated. |
| **Terra** | Situation assessment against the COP (operator Analyze) | Stronger reasoning models when the human asks for assessment; evidence-cited insights only. |
| **Sol** | Recipient-facing briefings / recommendations (explicit request) | Best-of-class drafting when a human triggers a deep briefing — never automatic firehose. |

**Live path today:** server-only `OPENAI_API_KEY` + per-agent
`MOSAIC_*_PROVIDER=live|fixture` + `MOSAIC_SIM_MODE=live|record|replay|fixture`.
Live means real OpenAI clients on **operator model routes**; failures and
refusals are recorded as model runs and **do not** mutate the COP. Fixture means
deterministic demo-pack behaviour with no network call. Replay replays banked
cassette responses with no API cost.

**Why “boost OpenAI” here:**

- **Right model for the job** — route volume (Luna) vs depth (Terra/Sol) instead
  of one expensive call for every tick.  
- **Structured, schema-validated outputs** — agents return typed artifacts that
  the core can store and supersede, not free-form UI soup.  
- **Operator-triggered spend** — Analyze/brief/interpret are explicit actions;
  Play itself is not an OpenAI firehose.  
- **Auditability** — every invocation is a first-class model run (provider,
  inputs, validation status, outputs).  
- **Swap without rewiring** — change provider mode or, later, model class per
  agent without touching the projector or UI contracts.

Details: [`docs/live-models.md`](docs/live-models.md).

---

## Same pipeline, other applications

**Architecture intent — not a shipped multi-domain product today.** The
domestic-disturbance package is a **reference domain profile**. Developers are
meant to plug another **deterministic core / event feed** and keep agents,
audit, and streams.

| Domain | Their deterministic core | Mosaic adds |
|--------|--------------------------|-------------|
| **Enterprise cybersecurity** | SIEM, asset inventory, tickets, control-plane facts | Risk assessment agents, human approve, full provenance; COP never silently rewritten |
| **Government disaster management** | EOC resources, hazards, shelter capacity, roads/bridges | Streamed picture, advisory agents, recorded handoffs (not auto-executed) |
| **This demo** | Synthetic 911-style incident + environment | Dense reference flow you can play end-to-end |

**Handoff seam:** controls like “save maintenance note (demo)” record
*intent* only. In a multi-domain Mosaic, that is where you would plug another
profile, CMMS, cyber ticket bus, or EOC channel — delivery stays
policy-governed and outside the agent.

---

## Try the reference demo

### Synthetic data

The checked-in `datasets/domestic-disturbance` fixture is enough for a full
walkthrough (10 beats → COP through state revision 9, historical advisories,
integrity paths, sample audits). No extra data generation required.

### Quick walkthrough (5–8 minutes)

1. Open [https://mosaic.nryn.dev](https://mosaic.nryn.dev) — confirm **Connected**.  
   The board is **empty** until you play (progressive mode).  
2. Note agent / cassette mode from the UI (fixture pack vs live / recording /
   replay — process env, not a UI toggle).  
3. **Play scenario** — watch facts stream onto the board as the real pipeline
   advances (COP revisions climb toward 9). This path does **not** spend OpenAI
   credits by itself.  
4. **Show source** on a claim; open **Model Actions** after COP is ready for
   live Luna/Terra/Sol (or rely on fixture continuum during Play). Discuss
   supersession after the road opens.  
5. Save a Dispatch or maintenance note — **not carried out / not delivered**.  
6. Open **Decision history** for the paper trail (including invalid/refused
   model runs).

Automated capture: [`ui/e2e`](ui/e2e) + [`docs/runbook/playwright-demo-e2e.md`](docs/runbook/playwright-demo-e2e.md).

---

## Local development

Copy [`.env.example`](.env.example) to root `.env` (gitignored) and fill in:

```bash
OPENAI_API_KEY=sk-proj-your-openai-api-key-here

# Compose defaults: providers=live, MOSAIC_SIM_MODE=live, local Postgres.
# For shared durable store with Cloud Run (session pooler, IPv4):
# MOSAIC_DB_PATH=postgres://postgres.<ref>:PASSWORD@aws-0-<region>.pooler.supabase.com:5432/postgres?sslmode=require

# Safe $0 demo even with a key present:
# MOSAIC_SIM_MODE=fixture
# MOSAIC_LUNA_PROVIDER=fixture
# MOSAIC_TERRA_PROVIDER=fixture
# MOSAIC_SOL_PROVIDER=fixture
```

```bash
docker compose up --build
```

Open [http://localhost:8080](http://localhost:8080).

- **Default store:** local Postgres container (`db` service).  
- **Shared with Cloud Run:** set `MOSAIC_DB_PATH` to the Supabase **session**
  pooler DSN (port **5432**, user `postgres.<ref>`). Do **not** use transaction
  pooler port `6543`. Free-tier direct hosts are often IPv6-only.

### Port binding

1. `MOSAIC_LISTEN_ADDR` — explicit listen address (e.g. `:8080`)  
2. `PORT` — if listen addr empty, bind `0.0.0.0:${PORT}` (Cloud Run)  
3. Default — `127.0.0.1:8080`  

The production image does not bake in `MOSAIC_LISTEN_ADDR`. Compose sets
`:8080` explicitly.

---

## Cloud Run + Supabase (durable demo)

**What it is:** live, single-instance, durable Postgres via **Supabase**
(session pooler). Local Compose and Cloud Run can share one DSN so local Plays
show up on the hosted demo and vice versa.

**What it is not:** multi-region HA or Kafka. Cassette files under `/tmp` on
Cloud Run are still process-local.

```bash
gcloud services enable \
  artifactregistry.googleapis.com \
  run.googleapis.com \
  secretmanager.googleapis.com
gcloud auth configure-docker us-central1-docker.pkg.dev
```

Store secrets in Secret Manager (not plain `--set-env-vars` for credentials):

```bash
printf '%s' "$OPENAI_API_KEY" | gcloud secrets create openai-api-key \
  --data-file=- \
  --replication-policy=automatic

# Session pooler DSN (IPv4). Do NOT use transaction pooler :6543.
printf '%s' 'postgres://postgres.<ref>:PASSWORD@aws-0-<region>.pooler.supabase.com:5432/postgres?sslmode=require' \
  | gcloud secrets create mosaic-db-dsn --data-file=-

PROJECT_NUMBER="$(gcloud projects describe "$(gcloud config get-value project)" --format='value(projectNumber)')"
for s in openai-api-key mosaic-db-dsn; do
  gcloud secrets add-iam-policy-binding "$s" \
    --member="serviceAccount:${PROJECT_NUMBER}-compute@developer.gserviceaccount.com" \
    --role="roles/secretmanager.secretAccessor"
done
```

```bash
docker tag mosaic-demo:local us-central1-docker.pkg.dev/YOUR_PROJECT_ID/mosaic-repo/mosaic-demo:latest
docker push us-central1-docker.pkg.dev/YOUR_PROJECT_ID/mosaic-repo/mosaic-demo:latest

gcloud run deploy mosaic-demo \
  --image=us-central1-docker.pkg.dev/YOUR_PROJECT_ID/mosaic-repo/mosaic-demo:latest \
  --max-instances=1 \
  --concurrency=8 \
  --set-env-vars="MOSAIC_LUNA_PROVIDER=live,MOSAIC_TERRA_PROVIDER=live,MOSAIC_SOL_PROVIDER=live,MOSAIC_SIM_MODE=live,MOSAIC_CASSETTE_DIR=/tmp/mosaic-recordings" \
  --set-secrets="MOSAIC_DB_PATH=mosaic-db-dsn:latest,OPENAI_API_KEY=openai-api-key:latest" \
  --allow-unauthenticated \
  --region=us-central1
```

Local parity: put the **same** session-pooler DSN in gitignored `.env` as
`MOSAIC_DB_PATH=…` so `docker compose up` talks to Supabase (see
[`.env.example`](.env.example)).

Image includes versioned prompts under `prompts/{luna,terra,sol}/` and
`testdata/demo/` (recording manifest + cassettes). Full notes:
[`docs/runbook/cloud-run-deployment-analysis.md`](docs/runbook/cloud-run-deployment-analysis.md) §6.

* **Public URL:** [https://mosaic.nryn.dev](https://mosaic.nryn.dev)  
* **Cloud Run:** [https://mosaic-demo-358513274447.us-central1.run.app](https://mosaic-demo-358513274447.us-central1.run.app)

---

## What we do not claim

- Multi-tenant hosted platform or “AI ran the operation”  
- Real dispatch, real PII, or real external delivery  
- Multi-region HA or Kafka event transport today (Postgres spine is the demo path)  
- A shipped multi-domain product — only pluggable **architecture**  
- That **Play** alone is a paid OpenAI run (Model Actions are)

---

## Further reading

| Doc | Purpose |
|-----|---------|
| [`docs/live-models.md`](docs/live-models.md) | Fixture vs live agent configuration |
| [`docs/runbook/local-docker-demo.md`](docs/runbook/local-docker-demo.md) | Local Docker verification |
