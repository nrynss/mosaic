// Command mosaic runs the local Mosaic demo application and developer utilities.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "mosaic:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Println("Mosaic demo foundation")
		fmt.Println("Commands: quality, format")
		return nil
	}

	root, err := findModuleRoot()
	if err != nil {
		return err
	}

	switch args[0] {
	case "quality":
		if err := checkFormatting(root); err != nil {
			return err
		}
		if err := runCommand(root, "go", "vet", "./..."); err != nil {
			return err
		}
		return runCommand(root, "go", "test", "./...")
	case "format":
		files, err := goFiles(root)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return nil
		}
		return runCommand(root, "gofmt", append([]string{"-w"}, files...)...)
	default:
		return fmt.Errorf("unknown command %q (expected quality or format)", args[0])
	}
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

func checkFormatting(root string) error {
	files, err := goFiles(root)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}

	cmd := exec.Command("gofmt", append([]string{"-l"}, files...)...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("check formatting: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if unformatted := strings.TrimSpace(string(output)); unformatted != "" {
		return fmt.Errorf("Go files need formatting:\n%s\nrun: go run ./cmd/mosaic format", unformatted)
	}
	return nil
}

func goFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".worktrees", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || filepath.Ext(path) != ".go" {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("find Go files: %w", err)
	}
	return files, nil
}

func runCommand(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}
