# Google Cloud Run Deployment Analysis (Free Tier)

This document provides a detailed analysis of deploying the Mosaic single-instance demonstration application on Google Cloud Run within the GCP Free Tier limits.

---

## 1. Google Cloud Free Tier Eligibility

Google Cloud Run offers a generous permanent free tier that renews monthly. The limits are:

| Metric | Monthly Free Tier Limit | Mosaic Usage & Bounding |
|---|---|---|
| **CPU** | 180,000 vCPU-seconds | ~50 hours of active execution on 1 vCPU (scales to 0 when idle). |
| **Memory** | 360,000 GB-seconds | ~100 hours of active execution at 1 GB RAM (scales to 0 when idle). |
| **Requests** | 2 million requests | More than sufficient for operator and E2E checks. |
| **Egress** | 1 GiB free egress | Shared across GCP services. Sufficient for standard UI/API payloads. |
| **GCR Artifact Registry** | 500 MB storage | Builds easily fit within this container storage envelope. |

### The "Scale-to-Zero" Multiplier
Because Cloud Run supports **scaling to zero instances** when there is no incoming web traffic, compute seconds are only consumed during active request processing. For a demonstration app, this practically guarantees **$0.00** monthly compute costs.

---

## 2. The SQLite Persistence Challenge

Cloud Run container instances are **stateless and ephemeral**. The instance's local filesystem is a writable `tmpfs` volume, meaning:
* SQLite writes (like `mosaic.db`) succeed at runtime.
* However, when Cloud Run scales down to 0 instances (due to inactivity) or restarts/replaces an instance, the SQLite file is **wiped**.
* Any operator audit trail or simulation sessions recorded will be lost.

To maintain persistent operator decisions under the Free Tier, we have two primary options:

### Option A: Cloud Storage FUSE Volume Mount (Recommended for Demo)
Cloud Run supports mounting a Google Cloud Storage (GCS) bucket directly as a directory volume using GCSFuse. 
* **Cost**: GCS offers **5 GB** of free standard storage, 5,000 Class A (write/list) operations, and 50,000 Class B (read) operations per month.
* **Mechanism**: Mount a bucket at `/var/lib/mosaic` and point the SQLite path to it (`/var/lib/mosaic/mosaic.db`).
* **Pros**: Zero code changes. The SQLite file is saved directly to GCS.
* **Cons**: SQLite over FUSE has high latency for concurrent writes due to network-backed block updates. For a single-operator demo, this is acceptable.

### Option B: SQLite + Litestream Replicator
[Litestream](https://litestream.io/) runs as a sidecar process inside the container. It streams SQLite write-ahead log (WAL) changes to a GCS bucket.
* **Mechanism**: The container runs Litestream as its entrypoint, restores the db from GCS on boot, starts `mosaicdemo`, and replicates changes back to GCS.
* **Pros**: Excellent read/write performance (local SQLite speeds) and high reliability.
* **Cons**: Requires modifying the container packaging (`Dockerfile` and entrypoint script) to bundle the `litestream` binary and write-ahead log configuration.

---

## 3. Configuration & Port Adaptations

Cloud Run forces the container to listen on the port specified by the dynamic `PORT` environment variable (usually `8080` but not guaranteed). 

### Port Configuration in `mosaicdemo`
Our `main.go` parses the port from the `MOSAIC_LISTEN_ADDR` variable:
```go
listen := flags.String("listen-addr", valueOrDefault(getenv("MOSAIC_LISTEN_ADDR"), defaultListenAddress), "HTTP listen address")
```

To run seamlessly on Cloud Run, we can adapt this dynamically by setting the Cloud Run service environment variable:
* **`MOSAIC_LISTEN_ADDR`** = `:${PORT}`

Or we can patch `main.go` to automatically fallback to `PORT` if `MOSAIC_LISTEN_ADDR` is empty:
```diff
- listen := flags.String("listen-addr", valueOrDefault(getenv("MOSAIC_LISTEN_ADDR"), defaultListenAddress), "HTTP listen address")
+ listenPort := valueOrDefault(getenv("MOSAIC_LISTEN_ADDR"), "")
+ if listenPort == "" {
+ 	if p := getenv("PORT"); p != "" {
+ 		listenPort = ":" + p
+ 	} else {
+ 		listenPort = defaultListenAddress
+ 	}
+ }
+ listen := flags.String("listen-addr", listenPort, "HTTP listen address")
```

---

## 4. Implementation Steps for Deploying

1. **Create a GCS Bucket**:
   Create a standard storage bucket (e.g. `mosaic-demo-db-store`) in your GCP project.
2. **Push Image**:
   Build the image locally and push to Google Artifact Registry:
   ```bash
   docker tag mosaic-demo:local gcr.io/your-project/mosaic-demo:latest
   docker push gcr.io/your-project/mosaic-demo:latest
   ```
3. **Deploy with Volume Mount**:
   Deploy to Cloud Run, specifying the volume mount to map the GCS bucket to `/var/lib/mosaic` and mapping the port:
   ```bash
   gcloud run deploy mosaic-service \
     --image=gcr.io/your-project/mosaic-demo:latest \
     --add-volume=name=db-volume,type=cloud-storage,bucket=mosaic-demo-db-store \
     --add-volume-mount=volume=db-volume,mount-path=/var/lib/mosaic \
     --set-env-vars=MOSAIC_DB_PATH=/var/lib/mosaic/mosaic.db,MOSAIC_LISTEN_ADDR=:${PORT} \
     --allow-unauthenticated \
     --region=us-central1
   ```
