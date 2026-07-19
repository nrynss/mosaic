# Synthetic dataset generation

`datasetgen` is a data-production tool, not part of the Mosaic runtime. For the
P29 demonstration candidate it uses Cerebras `gemma-4-31b` to produce one
**staged candidate**. The demo, Docker image, Luna, Terra, and Sol do not call
Cerebras, load a model, or invoke this command.

Only the versioned synthetic prompt and read-only Mosaic schemas are sent to the
provider. Do not place real operational records, personal data, credentials, or
unreviewed model output in the repository. Generated candidates live only under
ignored `localmodels/staging/` until a later, explicitly approved promotion.

## P29 provider and request budget

P29 permits only the public Cerebras Chat Completions endpoint and the model
`gemma-4-31b`. Its credential is read only from `CEREBRAS_API_KEY` in the
current process; it is never accepted as a command-line flag, recorded in
provenance, or written to disk.

The budget is deliberately small:

- one no-data readiness smoke;
- at most one fixed-seed candidate request; and
- no automatic retries.

Stop after a rate limit, timeout, refusal, transport failure, or invalid model
output. Start a new approved attempt instead of retrying in a loop. The command
uses a 90-second request deadline and a maximum completion budget of 12,288
tokens.

## Reproducible promotion workflow

The model response itself can vary. The repeatable process is the recorded
provider/model identity, versioned prompt, fixed seed, bounded request
parameters, candidate validation, human review, and explicit freeze promotion.

1. Open a terminal where `CEREBRAS_API_KEY` is available, without echoing or
   committing it. Confirm the existing frozen fixture first:

   ```powershell
   if ([string]::IsNullOrWhiteSpace($env:CEREBRAS_API_KEY)) {
     throw 'CEREBRAS_API_KEY must be present in this terminal'
   }
   go run ./cmd/datasetgen validate
   ```

2. Make the one candidate request into a new or empty ignored staging directory.
   `generate-cerebras` is deliberately fixed to `gemma-4-31b`; it exposes no
   provider, endpoint, model, credential, or retry override.

   ```powershell
   go run ./cmd/datasetgen generate-cerebras `
     --prompt "prompts\datasetgen\v1.md" `
     --stage "localmodels\staging\domestic-disturbance-v2" `
     --scenario domestic-disturbance `
     --seed 20260720
   ```

   The command compiles the current read-only schemas, builds a bounded
   synthetic-only prompt, makes exactly one non-streaming request, and accepts
   only one strict JSON artifact bundle. It writes only under `--stage`:

   ```text
   <stage>/
   ├── artifacts/
   │   ├── manifest.json
   │   ├── scenario.json
   │   ├── raw-events.json
   │   └── expected-outcomes.json
   ├── model-output.json
   └── provenance.json
   ```

   `provenance.json` records the Cerebras endpoint and model ID, prompt path and
   checksum, scenario and seed, sanitized request parameters (including the
   no-retry policy), generation timestamp, prompt-input and model-output
   checksums, and schema versions. It does not contain an API key, authorization
   header, full request prompt, or provider error body.

3. Review the staged JSON. A candidate is intentionally not admitted to
   `datasets/` merely because it parses. Verify that it is synthetic-only and
   that its manifest, scenario, event ordering, corrections, IDs, evidence, and
   expected outcomes are internally consistent. Invalid candidates remain in
   staging for inspection.

4. Do not freeze without explicit coordinator and user approval. When approval
   exists, freeze exactly one reviewed candidate into a new, versioned direct
   child of the repository `datasets/` directory. The target must not exist.

   ```powershell
   go run ./cmd/datasetgen freeze `
     --input "localmodels\staging\domestic-disturbance-v2" `
     --output "datasets\domestic-disturbance-v2"
   ```

   Freeze checks provenance completeness and checksums, proves the staged
   artifacts match the strict model response, validates all artifacts with the
   P04 validator and current P02 schemas, and then atomically creates the frozen
   target. It never changes or removes the staging directory, including on
   failure. It refuses a pre-existing target or any destination outside the
   repository `datasets/` root.

5. Validate frozen datasets with the normal offline gate:

   ```powershell
   go run ./cmd/datasetgen validate
   ```

The checked-in `datasets/domestic-disturbance/` fixture remains the frozen demo
input. Model generation is optional process evidence for future synthetic
versions, never a live source of operational data or a runtime dependency.