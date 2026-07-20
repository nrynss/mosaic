# Mosaic Bounded Demonstration App

Mosaic is a greenfield interactive operator timeline: synthetic scenario beat
replays, SSE streams, SQLite-backed immutable audit trails, and opt-in
generative models (Luna / Terra / Sol).

**Live demo:** [https://mosaic-demo-358513274447.us-central1.run.app](https://mosaic-demo-358513274447.us-central1.run.app)

In the UI, open **Help** (header or left rail) for the online walkthrough. Hover
any **?** mark for field tips.

---

## 1. Is the synthetic data enough?

**Yes.** The checked-in `datasets/domestic-disturbance` fixture is sufficient
for a complete hackathon operator demo. You do **not** need additional generated
data for the walkthrough.

| Asset | Coverage |
|-------|----------|
| **Scenario beats** | 10 timed events (911 call → road correction) |
| **Entities** | Incident, unit, EMS resource, two roads, weather alert, location |
| **Integrity paths** | Incomplete road repair, quarantined invalid input, late EMS delivery |
| **COP** | Deterministic projection through **state revision 9** |
| **Advisories** | Historical Terra insights (later superseded) + Sol recommendation |
| **Audits** | Sample briefing request + supervisor acknowledgement |
| **Simulation** | Same beats drive Start Simulation session replay |

Narrative arc operators can tell:

1. Domestic disturbance intake and welfare check  
2. Weather + road access constraints (Main Street / Brook Lane)  
3. EMS and officer updates  
4. Access assessment → recommendation (historical, then superseded)  
5. Road reopens → assessment no longer current  
6. Operator records handoffs / decisions without external delivery  

Full presenter script: [`docs/demo-script.md`](docs/demo-script.md).

---

## 2. Local Development Setup

### Bounded Env File Configuration
Create a `.env` at the repository root (gitignored):

```bash
# e:\work\mosaic\.env
OPENAI_API_KEY=sk-proj-your-openai-api-key-here
# Optional overrides (Compose defaults are live):
# MOSAIC_LUNA_PROVIDER=live
# MOSAIC_TERRA_PROVIDER=live
# MOSAIC_SOL_PROVIDER=live
```

Compose injects the key and defaults Luna/Terra/Sol to **live**. An empty key
falls back to **fixture** at process start. A zero-balance key still shows
`live`; failed API calls are recorded as model-run failures.

### Build and Run via Docker Compose

```bash
docker compose up --build
```

Open [http://localhost:8080](http://localhost:8080).

Compose mounts a named volume at `/var/lib/mosaic` so SQLite audits **survive**
local container restarts. That durability does **not** apply to Cloud Run
(`/tmp`).

### In-app help

* **Help** button in the masthead — topics for architecture, data, walkthrough,
  agents, COP/evidence, safety, and persistence.
* **?** hover tips on simulation controls, agent badges, Analyze, tabs, handoffs,
  and evidence resolution.
* Left rail: **Open help & walkthrough**.

---

## 3. Demo walkthrough (5–8 minutes)

1. Open the app; confirm connection **Live** and agent badges (`live` or `fixture`).
2. Click **Help** once if the audience is new — then close it.
3. **Start Simulation** — 10 synthetic beats replay; watch COP facts update.
4. **Resolve evidence** on a road or weather claim (right rail).
5. **Analyze Incident** — refresh advisory history; discuss superseded access
   assessment after the road opens.
6. Prepare a **Dispatch** or **Maintenance** handoff — show `executed: false`,
   `delivered: false`.
7. Open **Provenance & Action Trail** for model runs and audits.
8. **Reset** for a clean second pass if needed.

Say clearly: models inform; the deterministic projector alone mutates the COP;
no real department is contacted.

---

## 4. Port Binding & Environment Defaults

1. **`MOSAIC_LISTEN_ADDR`** — explicit listen address (e.g. `:8080`).
2. **`PORT`** — if listen addr is empty, bind `0.0.0.0:${PORT}` (Cloud Run).
3. **Default** — `127.0.0.1:8080`.

The production image does **not** bake in `MOSAIC_LISTEN_ADDR`. Compose sets
`MOSAIC_LISTEN_ADDR=:8080` explicitly.

---

## 5. Cloud Run Deployment (ephemeral hackathon demo)

**What it is:** live now, single-instance, fixture-safe, **ephemeral** `/tmp` DB.  
**What it is not:** Litestream / Cloud SQL durable history.

### Prerequisites

```bash
gcloud services enable \
  artifactregistry.googleapis.com \
  run.googleapis.com \
  secretmanager.googleapis.com
gcloud auth configure-docker us-central1-docker.pkg.dev
```

### Secret Manager for the API key

Do **not** pass the key via `--set-env-vars` (shell history risk).

```bash
printf '%s' "$OPENAI_API_KEY" | gcloud secrets create openai-api-key \
  --data-file=- \
  --replication-policy=automatic
# existing: gcloud secrets versions add openai-api-key --data-file=-

PROJECT_NUMBER="$(gcloud projects describe "$(gcloud config get-value project)" --format='value(projectNumber)')"
gcloud secrets add-iam-policy-binding openai-api-key \
  --member="serviceAccount:${PROJECT_NUMBER}-compute@developer.gserviceaccount.com" \
  --role="roles/secretmanager.secretAccessor"
```

### Push and deploy

```bash
docker tag mosaic-demo:local us-central1-docker.pkg.dev/YOUR_PROJECT_ID/mosaic-repo/mosaic-demo:latest
docker push us-central1-docker.pkg.dev/YOUR_PROJECT_ID/mosaic-repo/mosaic-demo:latest

gcloud run deploy mosaic-demo \
  --image=us-central1-docker.pkg.dev/YOUR_PROJECT_ID/mosaic-repo/mosaic-demo:latest \
  --max-instances=1 \
  --concurrency=8 \
  --set-env-vars="MOSAIC_DB_PATH=/tmp/mosaic.db,MOSAIC_LUNA_PROVIDER=live,MOSAIC_TERRA_PROVIDER=live,MOSAIC_SOL_PROVIDER=live" \
  --set-secrets="OPENAI_API_KEY=openai-api-key:latest" \
  --allow-unauthenticated \
  --region=us-central1
```

Leave `MOSAIC_LISTEN_ADDR` unset so Cloud Run `PORT` is used.

### Live service

* **URL:** [https://mosaic-demo-358513274447.us-central1.run.app](https://mosaic-demo-358513274447.us-central1.run.app)
* Durable SQLite (Litestream → GCS or Cloud SQL) remains a **future** parcel —
  see [`docs/runbook/cloud-run-deployment-analysis.md`](docs/runbook/cloud-run-deployment-analysis.md).

---

## 6. Further reading

| Doc | Purpose |
|-----|---------|
| [`docs/demo-script.md`](docs/demo-script.md) | Presenter script and talking points |
| [`docs/live-models.md`](docs/live-models.md) | Fixture vs live agent configuration |
| [`docs/runbook/local-docker-demo.md`](docs/runbook/local-docker-demo.md) | Local Docker verification |
| [`HANDOFF.md`](HANDOFF.md) | Integration board and live deploy status |
