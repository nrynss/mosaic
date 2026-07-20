# Mosaic Bounded Demonstration App

Mosaic is a greenfield interactive operator timeline application utilizing synthetic scenario beat replays, SSE streams, SQLite-backed immutable audit trails, and opt-in generative models.

---

## 1. Local Development Setup

### Bounded Env File Configuration
Before running the application locally or deploying it, create a `.env` file at the repository root. This file is ignored by Git to prevent secret exposure.

Add your OpenAI API key to the `.env` file to enable live model reasoning (Terra/Sol/Luna):
```bash
# e:\work\mosaic\.env
OPENAI_API_KEY=sk-proj-your-openai-api-key-here
```

### Build and Run via Docker Compose
To build and spin up the complete application (Go API server + Svelte dashboard + database seeding) locally:
```bash
docker compose up --build
```
The application will stand up at [http://localhost:8080](http://localhost:8080).

---

## 2. Port Binding & Environment Defaults

The Go process configures its port binding dynamically based on the following precedence rules:
1. **`MOSAIC_LISTEN_ADDR`**: Explicitly set listen address (e.g. `127.0.0.1:8080`).
2. **`PORT` (Cloud Run Fallback)**: If `MOSAIC_LISTEN_ADDR` is empty and the `PORT` environment variable is defined, the process automatically binds to `0.0.0.0:${PORT}` to satisfy Cloud Run's dynamic runtime health check requirements.
3. **Default**: Fallback to `127.0.0.1:8080`.

---

## 3. Google Cloud Run Deployment (Durable Design)

Due to process-local SSE streams and SQLite single-writer exclusivity, the application is deployed to Google Cloud Run under strict single-instance constraints.

### Prerequisites
1. Enable the Artifact Registry and Cloud Run APIs:
   ```bash
   gcloud services enable artifactregistry.googleapis.com run.googleapis.com
   ```
2. Configure Docker authentication:
   ```bash
   gcloud auth configure-docker us-central1-docker.pkg.dev
   ```

### Push Container to Artifact Registry
Use the Artifact Registry format (`LOCATION-docker.pkg.dev/PROJECT/REPOSITORY/IMAGE:TAG`) to tag and push the production build:
```bash
# Tag the local image
docker tag mosaic-demo:local us-central1-docker.pkg.dev/YOUR_PROJECT_ID/mosaic-repo/mosaic-demo:latest

# Push the image
docker push us-central1-docker.pkg.dev/YOUR_PROJECT_ID/mosaic-repo/mosaic-demo:latest
```

### Deploy to Cloud Run
Deploy the service using the single-instance constraints and point the database path to a writable `/tmp` directory:
```bash
gcloud run deploy mosaic-demo \
  --image=us-central1-docker.pkg.dev/YOUR_PROJECT_ID/mosaic-repo/mosaic-demo:latest \
  --max-instances=1 \
  --concurrency=1 \
  --set-env-vars="MOSAIC_DB_PATH=/tmp/mosaic.db,OPENAI_API_KEY=sk-proj-..." \
  --allow-unauthenticated \
  --region=us-central1
```

The active, live demonstration service is hosted on Google Cloud Run:
* **Live Service URL**: **[https://mosaic-demo-358513274447.us-central1.run.app](https://mosaic-demo-358513274447.us-central1.run.app)**

*Note: In the Cloud Run environment, the database is hosted at `/tmp/mosaic.db` (in-memory/tmpfs), which is ephemeral. For a permanent production persistence layer, configure a shared database (like Cloud SQL PostgreSQL) or set up Litestream WAL synchronization to a GCS bucket as outlined in the [Cloud Run Deployment Runbook](docs/runbook/cloud-run-deployment-analysis.md).*
