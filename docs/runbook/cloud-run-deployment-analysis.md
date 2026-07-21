# Google Cloud Run Deployment Analysis (Durable Design — Proposed)

This document evaluates deploying the Mosaic interactive operator demo on
Google Cloud Run, and records the **chosen durable demo path**: Cloud Run for
the app + **Supabase** (managed Postgres) for the store.

> **Status:** As of v0.4, the store is a pluggable event spine and the same
> image speaks Postgres when `MOSAIC_DB_PATH` is a `postgres://` DSN
> ([main.go](../../cmd/mosaicdemo/main.go) routes on the DSN prefix). The
> **chosen durable demo topology is Cloud Run + Supabase** (see §0 and §6).
> The old SQLite-under-`/tmp` ephemeral path (§5) and the Litestream/GCS
> single-writer parcel (§3–§5) are retained below as **superseded
> SQLite-only alternatives** — they predate the v0.4 Postgres spine and are
> not the deployment we run for the demo.

---

## 0. Deployment Models (the store is pluggable)

Mosaic's architecture deliberately **supports multiple deployment models** from
a single application image. The event spine and durable store are selected at
runtime by `MOSAIC_DB_PATH`; nothing in the app changes between topologies.

| Model | Store | `MOSAIC_DB_PATH` | Durability | Use |
|---|---|---|---|---|
| Local dev | SQLite file (or in-memory) | `./mosaic.db` | Local disk | Tests, quick runs |
| Docker Compose | Postgres container + volume | `postgres://…@db:5432/…` | Named volume | Full local end-to-end |
| **Cloud Run + Supabase** | Managed Postgres (Supabase) | `postgres://…@…supabase.co:5432/…?sslmode=require` | **Managed, durable** | **Chosen demo** |
| Cloud Run + Cloud SQL | Managed Postgres (GCP-native) | Cloud SQL DSN via connector | Managed, durable | Enterprise GCP path |
| VM / GKE | Literal `docker compose up` / StatefulSet | Postgres in-cluster | Volume | "Local Docker in the cloud" |

The Go E2E suite ([tests/e2e](../../tests/e2e)) is independent of all of these —
it starts the binary in-process against a SQLite temp file, so it runs anywhere
(including CI) without Postgres or Docker.

**For the demo we use GCR (Cloud Run) + Supabase.** Same image as Compose, real
durability, `$0` free tier. See §6 for the exact steps.

---

## 1. Architectural Options Evaluated

We evaluated two paths for deploying this application to Cloud Run:
1. **Ephemeral Cloud Run Demo (current live path)**: Run without a durable
   persistence layer. The database lives under `/tmp` and resets to the seed
   fixture whenever the container scales to zero or restarts. No persistence
   promise for audit/model history.
2. **Durable Cloud Run Demo (future parcel)**: Shared database or a formally
   designed single-writer backup-and-restore mechanism. Required only when we
   need immutable audit records and simulation history to survive restarts.

---

## 2. Persistence & Storage Boundaries

### Why Cloud Storage FUSE is Avoided
We explicitly **reject** placing an active SQLite database on a Google Cloud Storage (GCS) FUSE volume mount. 
* **POSIX & Locking Limitations**: Google Cloud Storage is an object store, and GCS FUSE does not support file locking, concurrency controls, or POSIX-compliant write semantics.
* **Integrity Risk**: Attempting to run an active database like SQLite over GCS FUSE leads to corruption, transaction failures, and breaks Mosaic's core auditability and immutability guarantees.
* **Permission Issues**: GCS FUSE mounts default to root ownership, which conflicts with our Docker nonroot security posture.

### Proposed durable path: Single-Writer Litestream Replication (Option 2B)
To achieve durable SQLite persistence under free-tier-friendly constraints, a
future parcel can use **[Litestream](https://litestream.io/)** replicating to a
standard GCS bucket. **None of this is in the current image or live service.**

* **Boot Phase**: On container startup, a custom entrypoint script calls `litestream restore` to fetch the latest database backup from the GCS bucket.
* **Runtime Phase**: The application runs and writes to a local, high-speed SQLite database file (still not GCS FUSE).
* **Replication Phase**: A lightweight background Litestream worker replicates WAL (write-ahead log) changes to GCS every second.
* **REST Communication**: Litestream uses standard GCS API calls rather than a filesystem mount, bypassing GCS FUSE file locking and nonroot ownership issues.

---

## 3. Concurrency & Scaling Constraints

Cloud Run is designed to scale horizontally by default. However, Mosaic uses process-local state (for simulation streams, best-effort SSE brokers) and a single-writer SQLite database.

To prevent write conflicts, split simulation states, and broken streams, **we must constrain the deployment to a single instance**:
* **Enforce Instance Limit**: Deploy the service with `--max-instances=1` to preserve single-writer database integrity.
* **Configure Concurrency**: Set `--concurrency=8` so that concurrent REST API requests (such as `/health` or UI checks) are not blocked by the persistent Server-Sent Events (SSE) stream occupying the single instance.

*Note: For a future production migration to horizontal scaling, the backend must transition to a shared store like Cloud SQL (PostgreSQL) and a distributed pub/sub broker.*

---

## 4. GCP Allowance and Budgeting

While the resources are designed to fit within GCP's permanent Always Free allowances, **$0 costs are not guaranteed**:
* **Limits & Caps**: GCS Always Free standard storage is limited to **5 GB** and is restricted to specific regions (e.g., `us-east1`, `us-west1`, `us-central1`).
* **Operation Tariffs**: GCS enforces monthly limits of 5,000 Class A (writes/updates) and 50,000 Class B (reads) operations. Frequent Litestream WAL syncs could approach these caps.
* **Budget Recommendation**: Do not assume the deployment is free. We **must** enforce a budget alert and billing cap in the Google Cloud Console to prevent unexpected charges.

---

## 5. Implementation Path (future durable parcel — not shipped)

The current `Dockerfile` is **distroless**, runs a single `mosaicdemo`
entrypoint, and contains **no** Litestream binary, `litestream.yml`, restore
script, or shell. Ephemeral deploy docs live in `README.md`. The steps below
are the proposed durable parcel only.

### Step 1: Package Litestream (image + entrypoint changes)

Distroless has no shell, so either:

* switch the final stage to a minimal shell image (e.g. `debian:bookworm-slim`
  nonroot), or
* use a multi-binary pattern with a custom static entrypoint that can exec
  Litestream then the app.

Sketch (requires a shell base, not current distroless):

```dockerfile
# Download and install Litestream (amd64 example; pin version + checksum)
ADD https://github.com/benbjohnson/litestream/releases/download/v0.3.13/litestream-v0.3.13-linux-amd64.tar.gz /tmp/litestream.tar.gz
RUN tar -C /usr/local/bin -xzf /tmp/litestream.tar.gz

COPY litestream.yml /etc/litestream.yml
COPY scripts/litestream-entrypoint.sh /usr/local/bin/litestream-entrypoint.sh
ENTRYPOINT ["/usr/local/bin/litestream-entrypoint.sh"]
```

Entrypoint responsibilities:

1. `litestream restore -if-replica-exists -o $MOSAIC_DB_PATH <replica-url>`
2. `exec litestream replicate -exec "/usr/local/bin/mosaicdemo …"`

Also need: GCS bucket, service-account IAM for the object prefix, budget alert,
and still `--max-instances=1` (single writer).

### Step 2: Push Image to Google Artifact Registry
Avoid legacy `gcr.io` paths. Use the modern Google Artifact Registry format:

```bash
# Build and tag using the regional docker repository
docker tag mosaic-demo:local us-central1-docker.pkg.dev/PROJECT_ID/mosaic-repo/mosaic-demo:latest

# Push image
docker push us-central1-docker.pkg.dev/PROJECT_ID/mosaic-repo/mosaic-demo:latest
```

### Step 3: Deploy to Cloud Run

**Ephemeral (current live posture)** — see `README.md`. Key points: `/tmp`
DB path, Secret Manager for `OPENAI_API_KEY`, no Litestream.

**Durable (future)** — same single-instance flags, plus GCS credentials /
workload identity for Litestream, a durable local path for the SQLite file
during the instance lifetime, and budget caps. Do **not** put the live SQLite
file on GCS FUSE.

```bash
gcloud run deploy mosaic-demo \
  --image=us-central1-docker.pkg.dev/PROJECT_ID/mosaic-repo/mosaic-demo:latest \
  --max-instances=1 \
  --concurrency=8 \
  --set-env-vars=MOSAIC_DB_PATH=/tmp/mosaic.db \
  --set-secrets=OPENAI_API_KEY=openai-api-key:latest \
  --allow-unauthenticated \
  --region=us-central1
```

Leave `MOSAIC_LISTEN_ADDR` unset so the process binds `0.0.0.0:${PORT}` via
`parseConfig` (the image no longer bakes `MOSAIC_LISTEN_ADDR`).

---

## 6. Chosen Demo Path — Cloud Run + Supabase (durable, $0)

Same image as Docker Compose; only the store moves off the ephemeral disk onto
managed Postgres. The app runs its own embedded migrations on startup
([pgstore/migrations.go](../../internal/pgstore/migrations.go)), so no schema
step is needed here.

### Step 1 — Get the Supabase Postgres DSN (via the CLI)

```bash
supabase login                          # one-time, opens browser
supabase projects list                  # find/confirm the project ref

# Retrieve the DIRECT connection string (NOT the transaction pooler).
# Dashboard equivalent: Project Settings → Database → Connection string → URI.
# It looks like:
#   postgres://postgres:[PASSWORD]@db.<project-ref>.supabase.co:5432/postgres
```

> **Two gotchas that will bite otherwise:**
> - Use the **direct** connection (port `5432`) or the **session** pooler —
>   **not** the transaction pooler (port `6543`). `pgx` prepared statements and
>   the startup migration DDL break under PgBouncer transaction pooling.
> - Append **`?sslmode=require`** to the DSN. (Compose uses `sslmode=disable`
>   against the in-network container; Supabase needs TLS.)

Final value: `postgres://postgres:PASSWORD@db.<ref>.supabase.co:5432/postgres?sslmode=require`

### Step 2 — Store the DSN as a Cloud Run secret

```bash
printf '%s' 'postgres://postgres:PASSWORD@db.<ref>.supabase.co:5432/postgres?sslmode=require' \
  | gcloud secrets create mosaic-db-dsn --data-file=-
# rotate later with: gcloud secrets versions add mosaic-db-dsn --data-file=-
```

### Step 3 — Deploy the same image, pointed at Supabase

```bash
gcloud run deploy mosaic-demo \
  --image=us-central1-docker.pkg.dev/PROJECT_ID/mosaic-repo/mosaic-demo:latest \
  --max-instances=1 \
  --concurrency=8 \
  --set-env-vars="MOSAIC_LUNA_PROVIDER=live,MOSAIC_TERRA_PROVIDER=live,MOSAIC_SOL_PROVIDER=live,MOSAIC_SIM_MODE=live,MOSAIC_CASSETTE_DIR=/tmp/mosaic-recordings" \
  --set-secrets="MOSAIC_DB_PATH=mosaic-db-dsn:latest,OPENAI_API_KEY=openai-api-key:latest" \
  --allow-unauthenticated \
  --region=us-central1
```

Notes:
- `--max-instances=1` / `--concurrency=8` still apply: process-local SSE/sim
  streams want a single writer even though Postgres itself is multi-writer safe.
- `MOSAIC_DB_PATH` now comes from the secret — do **not** also set it under
  `--set-env-vars` (last one wins / conflicts).
- `/tmp` remains writable on Cloud Run for cassette recordings; the rest of the
  filesystem is read-only, matching the Compose `read_only` + `tmpfs:/tmp`.

### Step 4 — Verify durability

Hit the deployed URL, run a Play, then force a new revision
(`gcloud run services update mosaic-demo --region=us-central1 --update-labels=redeploy=$(date +%s)`)
and confirm audit/model history survives the restart — the whole point of moving
off `/tmp`.

> **Supabase free-tier note:** projects pause after **7 consecutive days of
> inactivity** (data is retained; un-pause from the dashboard). An actively-used
> demo week stays live. Storage cap is 500 MB — far above this demo's footprint.
