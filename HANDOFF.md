# Mosaic Handoff Board

This is the central coordinator surface for the Mosaic project. The greenfield interactive operator demo increment (v0.3) is fully integrated, verified, and complete.

## Verification of the Interactive Operator Demo (v0.3)

All v0.3 parcels (P35–P47) have been merged and integrated into `main`. The implementation is fully verified across the entire stack:

1. **Simulation Control**: Starts, pauses, and ends synthetic session beat emission in configured scenario timing.
2. **Operator API & Key Safety**: Supports generative Analyze (Terra) and briefing (Sol) operator reviews under `executed: false` safety. Key is server-only.
3. **Department Handoffs**: Captures noted intent to recipient mailboxes (Dispatch/Maintenance) as `executed: false` and `delivered: false` audits without external side effects.
4. **Interactive UI Workspace**: Refactored to feature tabs for the incident timeline command panel, live elapsed counters, recurrence alerts, and detailed audit trails.
5. **Durable Persistence**: All model runs and operator audits persist in SQLite and are recovered upon process restarts.

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

## Cloud Run Deployment Analysis

The detailed evaluation and implementation path for running this demo on GCP's free tier (scale-to-zero compute, single-writer Litestream replication for SQLite persistence, and horizontal concurrency constraints) is documented in:
* **[Cloud Run Deployment Analysis Runbook](docs/runbook/cloud-run-deployment-analysis.md)**

The active, live demonstration service is hosted on Google Cloud Run:
* **Live Service URL**: **[https://mosaic-demo-358513274447.us-central1.run.app](https://mosaic-demo-358513274447.us-central1.run.app)**

---

## Next Steps

The next increment will focus on implementing the durable, single-writer backup-and-restore replication for the SQLite database:
* **Litestream Sidecar Packaging**: Update the production `Dockerfile` to bundle the Litestream binary and replication runner.
* **GCS Replication Bucket**: Provision the Google Cloud Storage bucket in an Always Free region (`us-central1`, `us-east1`, or `us-west1`).
* **Nonroot Access Setup**: Ensure the Litestream sidecar executes under the `nonroot:nonroot` UID/GID and inherits the GCS credentials securely.
* **Budget & Alerting**: Configure a GCP billing budget alert to ensure operation costs stay within free allowances.
