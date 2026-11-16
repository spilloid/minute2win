package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// An accidentally overlapping profile/data directory must not turn state,
// report capabilities or account hashes into a viewer-readable "profile".
func (s *Server) protectedProfile(name string) bool {
	path, err := filepath.Abs(filepath.Join(s.ManifestDir, name+".json"))
	if err != nil {
		return true
	}
	protected := []string{s.StatePath}
	if s.Auth != nil {
		protected = append(protected, s.Auth.Path)
	}
	if s.MCP != nil {
		protected = append(protected, s.MCP.SettingsPath)
	}
	info, _ := os.Stat(path)
	for _, file := range protected {
		if file == "" {
			continue
		}
		absolute, err := filepath.Abs(file)
		if err != nil {
			continue
		}
		if absolute == path || (runtime.GOOS == "windows" && strings.EqualFold(absolute, path)) {
			return true
		}
		other, err := os.Stat(absolute)
		if err == nil && info != nil && os.SameFile(info, other) {
			return true
		}
	}
	return false
}
