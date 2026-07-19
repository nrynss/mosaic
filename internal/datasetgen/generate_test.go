package datasetgen

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	response   []byte
	err        error
	calls      int
	executable string
	args       []string
}

func (runner *fakeRunner) Run(_ context.Context, executable string, args []string) ([]byte, error) {
	runner.calls++
	runner.executable = executable
	runner.args = append([]string(nil), args...)
	return append([]byte(nil), runner.response...), runner.err
}

func TestGenerateStagesOnlyCandidateAndCompleteProvenance(t *testing.T) {
	root := newTestRoot(t)
	response := frozenBundle(t, repositoryRoot(t), nil)
	runner := &fakeRunner{response: response}
	stage := filepath.Join(root, "localmodels", "staging", "domestic-disturbance-v2")
	config := generationConfig(t, root, stage, runner)
	config.Now = func() time.Time { return time.Date(2026, 7, 18, 10, 30, 0, 0, time.UTC) }

	provenance, err := Generate(root, config)
	if err != nil {
		t.Fatal(err)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.calls)
	}
	if runner.executable != config.LlamaPath {
		t.Fatalf("runner executable = %q, want %q", runner.executable, config.LlamaPath)
	}
	if !hasArgument(runner.args, "--ctx-size", llamaContextTokens) || !hasArgument(runner.args, "--n-predict", llamaPredictionTokens) || !hasArgument(runner.args, "--seed", "42") || !hasFlag(runner.args, "--single-turn") || !hasFlag(runner.args, "--simple-io") {
		t.Fatalf("runner args do not contain bounded generation controls: %q", runner.args)
	}
	promptArgument := argumentValue(t, runner.args, "-p")
	if !strings.Contains(promptArgument, "scenario_id: domestic-disturbance") || !strings.Contains(promptArgument, "raw-event.schema.json") {
		t.Fatal("bounded prompt omitted the scenario request or read-only schema input")
	}
	if provenance.RawResponseSHA256 != sha256Hex(response) {
		t.Fatalf("raw response checksum = %q", provenance.RawResponseSHA256)
	}
	if provenance.Prompt.Version != "1.0.0" || provenance.PromptInputSHA256 != sha256Hex([]byte(promptArgument)) {
		t.Fatalf("prompt provenance is incomplete: %#v", provenance.Prompt)
	}
	if provenance.Model.SHA256 != sha256Hex([]byte("test model")) || provenance.LlamaExecutable.SHA256 != sha256Hex([]byte("test llama")) {
		t.Fatalf("file provenance did not capture identities: %#v %#v", provenance.Model, provenance.LlamaExecutable)
	}
	if len(provenance.SchemaVersions) == 0 || len(provenance.CommandArgs) == 0 {
		t.Fatalf("provenance missing schema versions or command arguments: %#v", provenance)
	}
	if got := argumentValue(t, provenance.CommandArgs, "-p"); got != "sha256:"+sha256Hex([]byte(promptArgument)) {
		t.Fatalf("recorded prompt argument = %q", got)
	}

	for _, relative := range []string{
		StageModelOutputFile,
		StageProvenanceFile,
		filepath.Join(StageArtifactsDirectory, "manifest.json"),
		filepath.Join(StageArtifactsDirectory, "scenario.json"),
		filepath.Join(StageArtifactsDirectory, "raw-events.json"),
		filepath.Join(StageArtifactsDirectory, "expected-outcomes.json"),
	} {
		if _, err := os.Stat(filepath.Join(stage, relative)); err != nil {
			t.Fatalf("staged %s: %v", relative, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "datasets"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("generation wrote outside staging into datasets: %v", entries)
	}
}

func TestGenerateExtractsJSONObjectFromLocalCLIOutput(t *testing.T) {
	root := newTestRoot(t)
	stage := filepath.Join(root, "localmodels", "staging", "console-output")
	bundle := frozenBundle(t, repositoryRoot(t), nil)
	runner := &fakeRunner{response: append(append([]byte("Loading model...\n"), bundle...), []byte("\n[ Prompt: 10.0 t/s ]\n")...)}
	config := generationConfig(t, root, stage, runner)

	provenance, err := Generate(root, config)
	if err != nil {
		t.Fatal(err)
	}
	if provenance.RawResponseSHA256 != sha256Hex(bundle) {
		t.Fatalf("staged JSON checksum = %q, want %q", provenance.RawResponseSHA256, sha256Hex(bundle))
	}
	if got := mustReadFile(t, filepath.Join(stage, StageModelOutputFile)); string(got) != string(bundle) {
		t.Fatalf("staged model output = %s, want extracted JSON only", got)
	}
}
func TestGenerateRejectsMalformedModelOutputWithoutArtifacts(t *testing.T) {
	root := newTestRoot(t)
	stage := filepath.Join(root, "localmodels", "staging", "malformed")
	runner := &fakeRunner{response: []byte("not a JSON artifact bundle")}
	config := generationConfig(t, root, stage, runner)

	if _, err := Generate(root, config); err == nil || !strings.Contains(err.Error(), "model output") {
		t.Fatalf("Generate error = %v, want malformed output error", err)
	}
	entries, err := os.ReadDir(stage)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("malformed output wrote staged artifacts: %v", entries)
	}
}

func TestGenerateRequiresExistingLocalModel(t *testing.T) {
	root := newTestRoot(t)
	stage := filepath.Join(root, "localmodels", "staging", "missing-model")
	runner := &fakeRunner{response: frozenBundle(t, repositoryRoot(t), nil)}
	config := generationConfig(t, root, stage, runner)
	config.ModelPath = filepath.Join(root, "localmodels", "missing.gguf")

	if _, err := Generate(root, config); err == nil || !strings.Contains(err.Error(), "local model") {
		t.Fatalf("Generate error = %v, want absent local model error", err)
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls = %d, want no model invocation", runner.calls)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("absent model created stage: %v", err)
	}
}

func TestFreezePromotesValidatedCandidateAndPreservesStage(t *testing.T) {
	root := newTestRoot(t)
	stage := generateCandidate(t, root, nil)
	beforeOutput := mustReadFile(t, filepath.Join(stage, StageModelOutputFile))
	beforeProvenance := mustReadFile(t, filepath.Join(stage, StageProvenanceFile))
	output := filepath.Join(root, "datasets", "domestic-disturbance-v2")

	if err := Freeze(root, FreezeConfig{InputDir: stage, OutputDir: output}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.json", "scenario.json", "raw-events.json", "expected-outcomes.json"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("frozen %s: %v", name, err)
		}
	}
	if got := mustReadFile(t, filepath.Join(stage, StageModelOutputFile)); string(got) != string(beforeOutput) {
		t.Fatal("freeze changed model output in staging")
	}
	if got := mustReadFile(t, filepath.Join(stage, StageProvenanceFile)); string(got) != string(beforeProvenance) {
		t.Fatal("freeze changed provenance in staging")
	}
}

func TestFreezeRefusesSchemaInvalidCandidate(t *testing.T) {
	root := newTestRoot(t)
	stage := generateCandidate(t, root, func(files *artifactFiles) {
		files.Manifest = []byte(strings.Replace(string(files.Manifest), `"schema_version": "1.0.0"`, `"schema_version": "0.0.0"`, 1))
	})
	output := filepath.Join(root, "datasets", "invalid-candidate-v2")
	before := mustReadFile(t, filepath.Join(stage, StageModelOutputFile))

	if err := Freeze(root, FreezeConfig{InputDir: stage, OutputDir: output}); err == nil || !strings.Contains(err.Error(), "validate candidate artifacts") {
		t.Fatalf("Freeze error = %v, want schema validation failure", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("invalid candidate created output: %v", err)
	}
	if got := mustReadFile(t, filepath.Join(stage, StageModelOutputFile)); string(got) != string(before) {
		t.Fatal("freeze failure changed staging")
	}
}

func TestFreezeRefusesExistingOutput(t *testing.T) {
	root := newTestRoot(t)
	stage := generateCandidate(t, root, nil)
	output := filepath.Join(root, "datasets", "existing-candidate-v2")
	if err := os.Mkdir(output, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "sentinel"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := mustReadFile(t, filepath.Join(stage, StageProvenanceFile))

	if err := Freeze(root, FreezeConfig{InputDir: stage, OutputDir: output}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Freeze error = %v, want existing-output refusal", err)
	}
	if got := string(mustReadFile(t, filepath.Join(output, "sentinel"))); got != "keep" {
		t.Fatalf("existing output changed to %q", got)
	}
	if got := mustReadFile(t, filepath.Join(stage, StageProvenanceFile)); string(got) != string(before) {
		t.Fatal("existing-output failure changed staging")
	}
}

func TestFreezeRefusesDestinationOutsideDatasets(t *testing.T) {
	root := newTestRoot(t)
	stage := generateCandidate(t, root, nil)
	output := filepath.Join(root, "outside-v2")
	before := mustReadFile(t, filepath.Join(stage, StageProvenanceFile))

	if err := Freeze(root, FreezeConfig{InputDir: stage, OutputDir: output}); err == nil || !strings.Contains(err.Error(), "direct child") {
		t.Fatalf("Freeze error = %v, want destination containment failure", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("outside destination created: %v", err)
	}
	if got := mustReadFile(t, filepath.Join(stage, StageProvenanceFile)); string(got) != string(before) {
		t.Fatal("destination-containment failure changed staging")
	}
}

func TestFreezeRefusesIncompleteProvenance(t *testing.T) {
	root := newTestRoot(t)
	stage := generateCandidate(t, root, nil)
	provenancePath := filepath.Join(stage, StageProvenanceFile)
	var provenance Provenance
	if err := readStrictJSON(provenancePath, &provenance); err != nil {
		t.Fatal(err)
	}
	provenance.CommandArgs = nil
	content, err := json.Marshal(provenance)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(provenancePath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "datasets", "incomplete-provenance-v2")

	if err := Freeze(root, FreezeConfig{InputDir: stage, OutputDir: output}); err == nil || !strings.Contains(err.Error(), "provenance") {
		t.Fatalf("Freeze error = %v, want incomplete provenance refusal", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("incomplete provenance created output: %v", err)
	}
}

func generateCandidate(t *testing.T, root string, mutate func(*artifactFiles)) string {
	t.Helper()
	stage := filepath.Join(root, "localmodels", "staging", "candidate")
	runner := &fakeRunner{response: frozenBundle(t, repositoryRoot(t), mutate)}
	if _, err := Generate(root, generationConfig(t, root, stage, runner)); err != nil {
		t.Fatal(err)
	}
	return stage
}

func generationConfig(t *testing.T, root, stage string, runner *fakeRunner) GenerateConfig {
	t.Helper()
	llamaPath := filepath.Join(root, "bin", "llama-cli")
	modelPath := filepath.Join(root, "localmodels", "model.gguf")
	promptPath := filepath.Join(root, "prompts", "datasetgen", "test.md")
	mustWriteFile(t, llamaPath, []byte("test llama"))
	mustWriteFile(t, modelPath, []byte("test model"))
	mustWriteFile(t, promptPath, []byte("<!-- mosaic-prompt-version: 1.0.0 -->\nSynthetic-only test prompt.\n"))
	return GenerateConfig{
		LlamaPath:  llamaPath,
		ModelPath:  modelPath,
		PromptPath: promptPath,
		StageDir:   stage,
		ScenarioID: "domestic-disturbance",
		Seed:       42,
		Runner:     runner,
		Now:        func() time.Time { return time.Date(2026, 7, 18, 10, 30, 0, 0, time.UTC) },
	}
}

func frozenBundle(t *testing.T, root string, mutate func(*artifactFiles)) []byte {
	t.Helper()
	dir := filepath.Join(root, "datasets", "domestic-disturbance")
	files := artifactFiles{
		Manifest:         mustReadFile(t, filepath.Join(dir, "manifest.json")),
		Scenario:         mustReadFile(t, filepath.Join(dir, "scenario.json")),
		RawEvents:        mustReadFile(t, filepath.Join(dir, "raw-events.json")),
		ExpectedOutcomes: mustReadFile(t, filepath.Join(dir, "expected-outcomes.json")),
	}
	if mutate != nil {
		mutate(&files)
	}
	content, err := json.Marshal(artifactBundle{BundleVersion: bundleVersion, ScenarioID: "domestic-disturbance", Artifacts: files})
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func newTestRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	copyDirectory(t, filepath.Join(repositoryRoot(t), "ontology"), filepath.Join(root, "ontology"))
	if err := os.Mkdir(filepath.Join(root, "datasets"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func copyDirectory(t *testing.T, source, destination string) {
	t.Helper()
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			copyDirectory(t, filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name()))
			continue
		}
		mustWriteFile(t, filepath.Join(destination, entry.Name()), mustReadFile(t, filepath.Join(source, entry.Name())))
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func hasArgument(args []string, flag, want string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == flag && args[index+1] == want {
			return true
		}
	}
	return false
}

func hasFlag(args []string, want string) bool {
	for _, argument := range args {
		if argument == want {
			return true
		}
	}
	return false
}

func argumentValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	for index := 0; index+1 < len(args); index++ {
		if args[index] == flag {
			return args[index+1]
		}
	}
	t.Fatalf("argument %q is absent from %q", flag, args)
	return ""
}

func mustWriteFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}
