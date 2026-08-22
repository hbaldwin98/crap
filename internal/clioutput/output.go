package clioutput

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func Validate(path string) error {
	if path == "" {
		return fmt.Errorf("path must not be empty")
	}
	clean := filepath.Clean(path)
	if clean == "." || filepath.Base(clean) == "." {
		return fmt.Errorf("path must name a file")
	}
	parent := filepath.Dir(clean)
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("access parent directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("parent is not a directory")
	}
	if _, _, err := destinationMode(clean); err != nil {
		return err
	}
	return nil
}

func Write(stdout io.Writer, path string, render func(io.Writer) error) error {
	if path == "" {
		return render(stdout)
	}
	if err := Validate(path); err != nil {
		return err
	}
	clean := filepath.Clean(path)
	mode, existed, err := destinationMode(clean)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(clean), "."+filepath.Base(clean)+"-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if existed {
		if err := temporary.Chmod(mode); err != nil {
			return fmt.Errorf("preserve destination permissions: %w", err)
		}
	}
	if err := render(temporary); err != nil {
		return fmt.Errorf("render temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if _, _, err := destinationMode(clean); err != nil {
		return err
	}
	replaced, err := replaceFile(temporaryPath, clean)
	if replaced {
		committed = true
	}
	if err != nil {
		return fmt.Errorf("replace destination: %w", err)
	}
	return nil
}

func destinationMode(path string) (os.FileMode, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("access destination: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 0, false, fmt.Errorf("destination must not be a symlink")
	}
	if info.IsDir() {
		return 0, false, fmt.Errorf("path is a directory")
	}
	if !info.Mode().IsRegular() {
		return 0, false, fmt.Errorf("destination is not a regular file")
	}
	return info.Mode().Perm(), true, nil
}
