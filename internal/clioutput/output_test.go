package clioutput

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteSafelyReplacesDestinationAndPreservesMode(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(destination, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := Write(&stdout, destination, func(writer io.Writer) error {
		_, err := io.WriteString(writer, "new")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" || stdout.Len() != 0 {
		t.Fatalf("destination = %q, stdout = %q", data, stdout.String())
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(destination)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o640 {
			t.Fatalf("destination mode = %o", got)
		}
	}
}

func TestWritePreservesDestinationOnRenderFailure(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "report.json")
	if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("render failed")
	err := Write(io.Discard, destination, func(writer io.Writer) error {
		_, _ = io.WriteString(writer, "partial")
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v", err)
	}
	data, readErr := os.ReadFile(destination)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "old" {
		t.Fatalf("destination = %q", data)
	}
	entries, readErr := os.ReadDir(directory)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary file was not removed: %v", entries)
	}
}

func TestValidateRejectsDirectoriesAndMissingParents(t *testing.T) {
	directory := t.TempDir()
	for _, path := range []string{directory, filepath.Join(directory, "missing", "report.json")} {
		if err := Validate(path); err == nil {
			t.Errorf("Validate(%q) succeeded", path)
		}
	}
}

func TestValidateRejectsExistingAndDanglingSymlinkDestinations(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, destination := range map[string]string{
		"existing": filepath.Join(directory, "existing-link.json"),
		"dangling": filepath.Join(directory, "dangling-link.json"),
	} {
		t.Run(name, func(t *testing.T) {
			linkTarget := target
			if name == "dangling" {
				linkTarget = filepath.Join(directory, "missing.json")
			}
			if err := os.Symlink(linkTarget, destination); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if err := Validate(destination); err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("Validate error = %v", err)
			}
			if err := Write(io.Discard, destination, func(writer io.Writer) error {
				_, err := io.WriteString(writer, "replacement")
				return err
			}); err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("Write error = %v", err)
			}
			if name == "existing" {
				data, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				if string(data) != "target" {
					t.Fatalf("target = %q", data)
				}
			}
		})
	}
}
