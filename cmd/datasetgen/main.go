// Command datasetgen validates frozen synthetic Mosaic datasets.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"mosaic.local/mosaic/internal/dataset"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "datasetgen:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 1 || (len(args) == 1 && args[0] != "validate") {
		return errors.New("usage: datasetgen [validate]")
	}
	root, err := findModuleRoot()
	if err != nil {
		return err
	}
	if err := dataset.Validate(root); err != nil {
		return err
	}
	fmt.Println("dataset domestic-disturbance: valid frozen artifacts")
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
