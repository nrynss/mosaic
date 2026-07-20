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

## Next Steps

The next increment will focus on deploying the interactive operator demo to **Google Cloud Run** for free (within the permanent GCP Free Tier):
* **GCS Bucket Setup**: Provision a Standard Google Cloud Storage bucket (qualifying for the 5 GB Free Tier).
* **Environment Adaptations**: Wire port-binding fallbacks for Cloud Run's dynamic `${PORT}` environment variable.
* **Artifact Push**: Build and push the production container image to Google Artifact Registry.
* **Cloud Run Deployment**: Spin up the service with a GCSFuse volume mount mapping the GCS bucket to `/var/lib/mosaic` for persistent SQLite storage.
