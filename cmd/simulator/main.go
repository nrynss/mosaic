// Command simulator runs the deterministic, fixture-backed Mosaic v0.1 demo.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"mosaic.local/mosaic/internal/dataset"
	"mosaic.local/mosaic/internal/simulator"
	"mosaic.local/mosaic/internal/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "simulator:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: simulator <run|replay|validate> [flags]")
	}
	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	root := flags.String("root", ".", "Mosaic repository root or a child directory")
	database := flags.String("db", simulator.DefaultDBPath(), "SQLite DSN or database path; defaults outside the repository")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	repositoryRoot, err := simulator.RepositoryRoot(*root)
	if err != nil {
		return err
	}
	if err := dataset.Validate(repositoryRoot); err != nil {
		return fmt.Errorf("validate frozen dataset: %w", err)
	}
	if command == "validate" {
		return printJSON(map[string]any{"validated": true, "scenario": simulator.DomesticDisturbance})
	}
	if command != "run" && command != "replay" {
		return fmt.Errorf("unknown command %q", command)
	}

	dsn := *database
	if !filepath.IsAbs(dsn) && dsn == simulator.DefaultDBPath() {
		dsn = simulator.DefaultDBPath()
	}
	databaseStore, err := store.Open(context.Background(), dsn)
	if err != nil {
		return err
	}
	defer databaseStore.Close()
	service, err := simulator.New(simulator.Config{
		Store:      databaseStore,
		SchemaDir:  filepath.Join(repositoryRoot, "ontology"),
		FixtureDir: filepath.Join(repositoryRoot, "datasets", simulator.DomesticDisturbance),
	})
	if err != nil {
		return err
	}
	if command == "run" {
		result, err := service.Run(context.Background())
		if err != nil {
			return err
		}
		return printJSON(result)
	}
	result, err := service.Recover(context.Background())
	if err != nil {
		return err
	}
	verification, err := service.Verify(context.Background())
	if err != nil {
		return err
	}
	return printJSON(map[string]any{
		"state_revision": result.StateRevision,
		"cop":            result.COP,
		"verification":   verification,
	})
}

func printJSON(value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Println(string(encoded))
	return err
}
