//go:build !windows

package main

import (
	"os"
	"path/filepath"
)

func replaceStateFile(tmp, target string) error {
	if err := os.Rename(tmp, target); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
