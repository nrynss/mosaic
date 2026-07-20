// Command datasetgen validates frozen synthetic Mosaic datasets and produces
// staged offline candidates through an explicitly supplied local llama.cpp.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"mosaic.local/mosaic/internal/datasetgen"
	"mosaic.local/mosaic/internal/reference/domesticdisturbance/dataset"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "datasetgen:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	root, err := findModuleRoot()
	if err != nil {
		return err
	}
	if len(args) == 0 || (len(args) == 1 && args[0] == "validate") {
		if err := dataset.Validate(root); err != nil {
			return err
		}
		fmt.Println("dataset domestic-disturbance: valid frozen artifacts")
		return nil
	}
	switch args[0] {
	case "generate":
		return runGenerate(root, args[1:], datasetgen.ExecRunner{})
	case "generate-cerebras":
		return runGenerateCerebras(root, args[1:])
	case "validate-stage":
		return runValidateStage(root, args[1:])
	case "freeze":
		return runFreeze(root, args[1:])
	default:
		return errors.New("usage: datasetgen validate | datasetgen generate --llama <path> --stage <dir> --scenario <id> --seed <integer> [--model <path>] [--prompt <path>] | datasetgen generate-cerebras --stage <dir> --scenario <id> --seed <integer> [--prompt <path>] | datasetgen validate-stage --input <stage> | datasetgen freeze --input <stage> --output <datasets/<scenario>-vN>")
	}
}

func runGenerate(root string, args []string, runner datasetgen.CommandRunner) error {
	flags := flag.NewFlagSet("generate", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	llamaPath := flags.String("llama", "", "path to local llama.cpp executable")
	modelPath := flags.String("model", datasetgen.DefaultModelPath, "path to local GGUF model")
	promptPath := flags.String("prompt", datasetgen.DefaultPromptPath, "path to versioned prompt")
	stageDir := flags.String("stage", "", "new or empty candidate staging directory")
	scenarioID := flags.String("scenario", "", "lowercase synthetic scenario identifier")
	seed := flags.Int64("seed", 0, "llama.cpp generation seed")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("generate accepts flags only")
	}
	provenance, err := datasetgen.Generate(root, datasetgen.GenerateConfig{
		LlamaPath:  *llamaPath,
		ModelPath:  *modelPath,
		PromptPath: *promptPath,
		StageDir:   *stageDir,
		ScenarioID: *scenarioID,
		Seed:       *seed,
		Runner:     runner,
	})
	if err != nil {
		return err
	}
	fmt.Printf("dataset candidate staged at %s (raw response sha256 %s)\n", *stageDir, provenance.RawResponseSHA256)
	return nil
}

func runGenerateCerebras(root string, args []string) error {
	flags := flag.NewFlagSet("generate-cerebras", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	promptPath := flags.String("prompt", datasetgen.DefaultPromptPath, "path to versioned prompt")
	stageDir := flags.String("stage", "", "new or empty candidate staging directory")
	scenarioID := flags.String("scenario", "", "lowercase synthetic scenario identifier")
	seed := flags.Int64("seed", 0, "fixed remote generation seed")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("generate-cerebras accepts flags only")
	}
	provenance, err := datasetgen.GenerateCerebras(root, datasetgen.CerebrasGenerateConfig{
		APIKey:     os.Getenv("CEREBRAS_API_KEY"),
		Model:      datasetgen.CerebrasGemmaModel,
		PromptPath: *promptPath,
		StageDir:   *stageDir,
		ScenarioID: *scenarioID,
		Seed:       *seed,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Cerebras dataset candidate staged at %s (raw response sha256 %s)\n", *stageDir, provenance.RawResponseSHA256)
	return nil
}

func runValidateStage(root string, args []string) error {
	flags := flag.NewFlagSet("validate-stage", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	inputDir := flags.String("input", "", "candidate staging directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("validate-stage accepts flags only")
	}
	if err := datasetgen.ValidateStage(root, *inputDir); err != nil {
		return err
	}
	fmt.Printf("staged dataset candidate at %s: valid without promotion\n", *inputDir)
	return nil
}

func runFreeze(root string, args []string) error {
	flags := flag.NewFlagSet("freeze", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	inputDir := flags.String("input", "", "candidate staging directory")
	outputDir := flags.String("output", "", "new versioned directory under datasets")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("freeze accepts flags only")
	}
	if err := datasetgen.Freeze(root, datasetgen.FreezeConfig{InputDir: *inputDir, OutputDir: *outputDir}); err != nil {
		return err
	}
	fmt.Printf("frozen validated dataset promoted to %s\n", *outputDir)
	return nil
}

func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("inspect module root: %w", err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("could not find go.mod")
		}
		dir = parent
	}
}
