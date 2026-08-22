//go:build !windows

package clioutput

import (
	"os"
	"path/filepath"
)

func replaceFile(source, destination string) (bool, error) {
	if err := os.Rename(source, destination); err != nil {
		return false, err
	}
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return true, err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return true, err
	}
	return true, nil
}
