package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPersistenceWithUnicodePathAndReplacement(t *testing.T) {
	f := newFixture(t, "")
	dir := filepath.Join(t.TempDir(), "déploiement-部署")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f.s.StatePath = filepath.Join(dir, "état.json")
	f.arm(t, "52:54:00:12:34:56")
	f.bootAndKey(t, "52:54:00:12:34:56")
	f.arm(t, "52:54:00:12:34:57")
	b, err := os.ReadFile(f.s.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	var saved state
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Armed) != 1 || len(saved.Deployments) != 1 {
		t.Fatalf("unexpected saved state: %s", b)
	}
	restarted := &Server{StatePath: f.s.StatePath}
	if err := restarted.Load(); err != nil {
		t.Fatal(err)
	}
	if len(restarted.st.Armed) != 0 || len(restarted.st.Deployments) != 1 {
		t.Fatalf("restart must preserve history and discard arms: %+v", restarted.st)
	}
}
