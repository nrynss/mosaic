# Offline synthetic dataset generation

`datasetgen` is a data-production tool, not part of the Mosaic runtime. It can
invoke a local Gemma GGUF through a locally installed `llama.cpp` executable to
produce a **staged candidate**. The demo, Docker image, Luna, Terra, and Sol do
not load the GGUF or invoke this command.

The model can be kept in the repository's ignored `localmodels/` directory. It
is intentionally not committed.

## Manual prerequisite

Download the specified GGUF manually, outside CI and only to the ignored local
model directory. The current Hugging Face CLI syntax is:

```powershell
hf download unsloth/gemma-4-E2B-it-GGUF gemma-4-E2B-it-UD-Q8_K_XL.gguf --local-dir localmodels
```

This command is an optional human prerequisite. `datasetgen` has no downloader,
no network code, and no credentials. Build or install `llama.cpp` separately;
its executable path is supplied explicitly for each generation run.

## Reproducible promotion workflow

The model response itself can vary. The repeatable process is the recorded
model/prompt identity, bounded command arguments, candidate validation, human
review, and explicit freeze promotion.

1. Generate only into a new or empty staging directory. Keeping staging under
   `localmodels/staging/` makes it ignored with the local model.

   ```powershell
   go run ./cmd/datasetgen generate `
     --llama "E:\llama.cpp\build\bin\Release\llama-cli.exe" `
     --model "localmodels\gemma-4-E2B-it-UD-Q8_K_XL.gguf" `
     --prompt "prompts\datasetgen\v1.md" `
     --stage "localmodels\staging\domestic-disturbance-v2" `
     --scenario domestic-disturbance `
     --seed 42
   ```

   The command reads the versioned prompt and current read-only ontology schemas,
   constrains llama.cpp with a context and completion limit, and accepts only a
   strict JSON artifact bundle. It writes only under `--stage`:

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

   `provenance.json` records the model and llama executable paths, SHA-256 values
   and sizes; prompt path, SHA-256, and embedded version; scenario and seed;
   sanitized command arguments; generation timestamp; prompt-input and raw-model-
   response checksums; and the complete schema-version map. No token or secret is
   accepted by the command or saved in provenance.

2. Review the staged JSON. A candidate is intentionally not admitted to
   `datasets/` merely because it parses. Invalid candidates remain in staging for
   inspection.

3. Freeze exactly one reviewed candidate into a new, versioned direct child of
   the repository `datasets/` directory. The target must not exist.

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

4. Validate frozen datasets with the normal offline gate:

   ```powershell
   go run ./cmd/datasetgen validate
   ```

The checked-in `datasets/domestic-disturbance/` fixture remains the frozen demo
input. Model generation is optional process evidence for future synthetic
versions, never a live source of operational data or a runtime dependency.