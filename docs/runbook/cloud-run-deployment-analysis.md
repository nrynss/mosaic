# Google Cloud Run Deployment Analysis (Durable Design)

This document provides a detailed evaluation of deploying the Mosaic interactive operator demo on Google Cloud Run under GCP free tier limits, focusing on the selected **durable, single-writer backup-and-restore architecture**.

---

## 1. Architectural Options Evaluated

We evaluated two paths for deploying this application to Cloud Run:
1. **Ephemeral Cloud Run Demo**: Run without a persistence layer. The database resets to the seed fixture whenever the container scales to zero or restarts. No persistence promise.
2. **Durable Cloud Run Demo (Selected Path)**: Shared database or a formally designed single-writer backup-and-restore mechanism. This is required to preserve immutable audit records and simulation history across restarts.

---

## 2. Persistence & Storage Boundaries

### Why Cloud Storage FUSE is Avoided
We explicitly **reject** placing an active SQLite database on a Google Cloud Storage (GCS) FUSE volume mount. 
* **POSIX & Locking Limitations**: Google Cloud Storage is an object store, and GCS FUSE does not support file locking, concurrency controls, or POSIX-compliant write semantics.
* **Integrity Risk**: Attempting to run an active database like SQLite over GCS FUSE leads to corruption, transaction failures, and breaks Mosaic's core auditability and immutability guarantees.
* **Permission Issues**: GCS FUSE mounts default to root ownership, which conflicts with our Docker nonroot security posture.

### Selected Solution: Single-Writer Litestream Replication (Option 2B)
To achieve durable SQLite persistence under the free tier, we will use **[Litestream](https://litestream.io/)** replicating to a standard GCS bucket:
* **Boot Phase**: On container startup, a custom entrypoint script calls `litestream restore` to fetch the latest database backup from the GCS bucket.
* **Runtime Phase**: The application runs and writes to a local, high-speed SQLite database file.
* **Replication Phase**: A lightweight background Litestream worker replicates WAL (write-ahead log) changes to GCS every second.
* **REST Communication**: Litestream uses standard GCS API calls rather than a filesystem mount, bypassing GCS FUSE file locking and nonroot ownership issues.

---

## 3. Concurrency & Scaling Constraints

Cloud Run is designed to scale horizontally by default. However, Mosaic uses process-local state (for simulation streams, best-effort SSE brokers) and a single-writer SQLite database.

To prevent write conflicts, split simulation states, and broken streams, **we must constrain the deployment to a single instance**:
* **Enforce Instance Limit**: Deploy the service with `--max-instances=1`.
* **Enforce Concurrency Limit**: Configure `--concurrency=1` to guarantee single-threaded request processing, or coordinate the model to prevent overlapping transactions.

*Note: For a future production migration to horizontal scaling, the backend must transition to a shared store like Cloud SQL (PostgreSQL) and a distributed pub/sub broker.*

---

## 4. GCP Allowance and Budgeting

While the resources are designed to fit within GCP's permanent Always Free allowances, **$0 costs are not guaranteed**:
* **Limits & Caps**: GCS Always Free standard storage is limited to **5 GB** and is restricted to specific regions (e.g., `us-east1`, `us-west1`, `us-central1`).
* **Operation Tariffs**: GCS enforces monthly limits of 5,000 Class A (writes/updates) and 50,000 Class B (reads) operations. Frequent Litestream WAL syncs could approach these caps.
* **Budget Recommendation**: Do not assume the deployment is free. We **must** enforce a budget alert and billing cap in the Google Cloud Console to prevent unexpected charges.

---

## 5. Implementation Path

### Step 1: Update Dockerfile for Litestream
To package Litestream, modify the runtime image stage in the `Dockerfile` to install the litestream binary and copy a `litestream.yml` configuration:

```dockerfile
# Download and install Litestream
ADD https://github.com/benbjohnson/litestream/releases/download/v0.3.13/litestream-v0.3.13-linux-amd64.tar.gz /tmp/litestream.tar.gz
RUN tar -C /usr/local/bin -xzf /tmp/litestream.tar.gz

# Configure litestream.yml
COPY litestream.yml /etc/litestream.yml
```

### Step 2: Push Image to Google Artifact Registry
Avoid legacy `gcr.io` paths. Use the modern Google Artifact Registry format:

```bash
# Build and tag using the regional docker repository
docker tag mosaic-demo:local us-central1-docker.pkg.dev/PROJECT_ID/mosaic-repo/mosaic-demo:latest

# Push image
docker push us-central1-docker.pkg.dev/PROJECT_ID/mosaic-repo/mosaic-demo:latest
```

### Step 3: Deploy to Cloud Run
Deploy the container with a maximum instance count of 1 and fallback port mapping:

```bash
gcloud run deploy mosaic-demo \
  --image=us-central1-docker.pkg.dev/PROJECT_ID/mosaic-repo/mosaic-demo:latest \
  --max-instances=1 \
  --concurrency=1 \
  --set-env-vars=MOSAIC_DB_PATH=/var/lib/mosaic/mosaic.db \
  --allow-unauthenticated \
  --region=us-central1
```
The Go process will dynamically bind to `0.0.0.0:${PORT}` at runtime using the PORT fallback configured in `parseConfig`.
