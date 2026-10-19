package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testProfile is a deployable profile: an image URL on an operator's own host and a (fake) pinned hash.
const testProfile = `{"schemaVersion":1,"image":{"source":"https","uri":"https://images.lab.internal/w11/install.wim",` +
	`"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","edition":"Windows 11 Pro"}}`

type fixture struct {
	s   *Server
	ts  *httptest.Server
	now time.Time
}

func newFixture(t *testing.T, token string) *fixture {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "win11-pro.json"), []byte(testProfile), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fixture{now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	f.s = &Server{ManifestDir: dir, ImageBase: "https://img.example/w11/", StatePath: filepath.Join(dir, "state.json"), Token: token,
		ArmTTL: time.Hour, ReportTTL: 3 * time.Hour, Now: func() time.Time { return f.now }}
	if err := f.s.Load(); err != nil {
		t.Fatal(err)
	}
	f.ts = httptest.NewServer(f.s.Handler())
	t.Cleanup(f.ts.Close)
	return f
}

func (f *fixture) do(t *testing.T, method, path, body, token string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, f.ts.URL+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json") // what curl scripts and the engine send
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r.StatusCode, string(b)
}

func (f *fixture) arm(t *testing.T, mac string) {
	t.Helper()
	if code, body := f.do(t, "POST", "/api/armed", `{"mac":"`+mac+`","manifest":"win11-pro"}`, ""); code != 200 {
		t.Fatalf("arm: %d %s", code, body)
	}
}

// bootAndKey simulates the target: network-boot (consuming the arm), then fetch the manifest to learn the report key.
func (f *fixture) bootAndKey(t *testing.T, mac string) string {
	t.Helper()
	f.do(t, "GET", "/boot/"+mac, "", "")
	_, body := f.do(t, "GET", "/manifest/"+mac, "", "")
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("manifest: %s", body)
	}
	return m["report"].(map[string]any)["key"].(string)
}

func TestUnarmedMachineBootsItsDisk(t *testing.T) {
	f := newFixture(t, "")
	_, body := f.do(t, "GET", "/boot/52:54:00:12:34:56", "", "")
	if !strings.Contains(body, "exit 1") || strings.Contains(body, "wimboot") {
		t.Fatalf("unarmed boot script should exit, got:\n%s", body)
	}
}

func TestOneArmIsOneWipe(t *testing.T) {
	f := newFixture(t, "")
	f.arm(t, "52-54-00-AB-CD-EF") // dashes and capitals normalise
	_, first := f.do(t, "GET", "/boot/52:54:00:ab:cd:ef", "", "")
	for _, want := range []string{"kernel https://img.example/w11/wimboot", "initrd --name manifest.json " + f.ts.URL + "/manifest/52:54:00:ab:cd:ef manifest.json", "initrd https://img.example/w11/boot.wim boot.wim", "boot\n"} {
		if !strings.Contains(first, want) {
			t.Fatalf("armed boot script missing %q:\n%s", want, first)
		}
	}
	_, second := f.do(t, "GET", "/boot/52:54:00:ab:cd:ef", "", "")
	if !strings.Contains(second, "exit 1") {
		t.Fatalf("second boot after the deploy must boot the disk, got:\n%s", second)
	}
}

func TestArmExpires(t *testing.T) {
	f := newFixture(t, "")
	f.arm(t, "52:54:00:12:34:56")
	f.now = f.now.Add(61 * time.Minute)
	if _, body := f.do(t, "GET", "/boot/52:54:00:12:34:56", "", ""); !strings.Contains(body, "exit 1") {
		t.Fatalf("expired arm still deploys:\n%s", body)
	}
}

func TestDisarm(t *testing.T) {
	f := newFixture(t, "")
	f.arm(t, "52:54:00:12:34:56")
	if code, _ := f.do(t, "DELETE", "/api/armed/52:54:00:12:34:56", "", ""); code != 204 {
		t.Fatal(code)
	}
	if _, body := f.do(t, "GET", "/boot/52:54:00:12:34:56", "", ""); !strings.Contains(body, "exit 1") {
		t.Fatal("disarmed machine still deploys")
	}
}

func TestArmRejectsBadInput(t *testing.T) {
	f := newFixture(t, "")
	for _, body := range []string{`{"mac":"nope","manifest":"win11-pro"}`, `{"mac":"52:54:00:12:34:56","manifest":"../../etc/passwd"}`, `{"mac":"52:54:00:12:34:56","manifest":"missing"}`, `{`} {
		if code, _ := f.do(t, "POST", "/api/armed", body, ""); code != 400 {
			t.Errorf("%s: got %d, want 400", body, code)
		}
	}
}

func TestTokenGuardsArming(t *testing.T) {
	f := newFixture(t, "s3cret")
	body := `{"mac":"52:54:00:12:34:56","manifest":"win11-pro"}`
	if code, _ := f.do(t, "POST", "/api/armed", body, ""); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	if code, _ := f.do(t, "POST", "/api/armed", body, "wrong"); code != 401 {
		t.Fatalf("wrong token: %d", code)
	}
	if code, _ := f.do(t, "POST", "/api/armed", body, "s3cret"); code != 200 {
		t.Fatalf("right token: %d", code)
	}
	if code, _ := f.do(t, "DELETE", "/api/armed/52:54:00:12:34:56", "", ""); code != 401 {
		t.Fatalf("disarm without token: %d", code)
	}
}

func TestManifestCarriesReportURL(t *testing.T) {
	f := newFixture(t, "")
	f.arm(t, "52:54:00:12:34:56")
	f.do(t, "GET", "/boot/52:54:00:12:34:56", "", "")
	_, body := f.do(t, "GET", "/manifest/52:54:00:12:34:56", "", "")
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	if m["report"].(map[string]any)["url"] != f.ts.URL+"/api/report" || m["schemaVersion"].(float64) != 1 || len(m["report"].(map[string]any)["key"].(string)) != 32 {
		t.Fatalf("manifest: %s", body)
	}
	if code, again := f.do(t, "GET", "/manifest/52:54:00:12:34:56", "", ""); code != 200 || again != body {
		t.Fatalf("an iPXE retry must get the same manifest: %d", code)
	}
	key := m["report"].(map[string]any)["key"].(string)
	f.do(t, "POST", "/api/report", `{"key":"`+key+`","macs":["52:54:00:12:34:56"],"phase":"ready","status":"running"}`, "")
	if code, _ := f.do(t, "GET", "/manifest/52:54:00:12:34:56", "", ""); code != 404 {
		t.Fatalf("manifest (and its key) still served after the target reported: %d", code)
	}
	if _, st := f.do(t, "GET", "/api/state", "", ""); strings.Contains(st, m["report"].(map[string]any)["key"].(string)) {
		t.Fatal("/api/state leaks the report key")
	}
	if code, _ := f.do(t, "GET", "/manifest/52:54:00:99:99:99", "", ""); code != 404 {
		t.Fatalf("never-booted MAC got a manifest: %d", code)
	}
}

func TestReports(t *testing.T) {
	f := newFixture(t, "")
	rep := func(key, status string) string {
		return `{"key":"` + key + `","macs":["AA:BB:CC:00:00:01","52:54:00:12:34:56"],"phase":"` + status + `","status":"` + status + `","model":"QEMU","timing":{"ready":2.5,"done":58.1}}`
	}
	if code, _ := f.do(t, "POST", "/api/report", rep("", "done"), ""); code != 404 {
		t.Fatalf("report before any boot accepted: %d", code)
	}
	f.arm(t, "52:54:00:12:34:56")
	key := f.bootAndKey(t, "52:54:00:12:34:56")
	if code, _ := f.do(t, "POST", "/api/report", rep("forged", "done"), ""); code != 404 {
		t.Fatalf("report with a wrong key accepted: %d", code)
	}
	if code, body := f.do(t, "POST", "/api/report", rep(key, "done"), ""); code != 204 {
		t.Fatalf("report: %d %s", code, body)
	}
	d := f.s.st.Deployments["52:54:00:12:34:56"]
	if d.Status != "done" || d.Timing["done"] != 58.1 || d.Model != "QEMU" {
		t.Fatalf("deployment not updated: %+v", d)
	}
	if code, _ := f.do(t, "POST", "/api/report", rep(key, "failed"), ""); code != 409 || d.Status != "done" {
		t.Fatalf("a finished deployment was overwritten: %d %s", code, d.Status)
	}
	f.now = f.now.Add(4 * time.Hour)
	if code, _ := f.do(t, "POST", "/api/report", rep(key, "running"), ""); code == 204 {
		t.Fatal("stale report accepted")
	}
}

func TestSaveFailureFailsClosed(t *testing.T) {
	f := newFixture(t, "")
	f.arm(t, "52:54:00:12:34:56")
	f.s.StatePath = filepath.Join(t.TempDir(), "missing-dir", "state.json") // writes now fail
	_, body := f.do(t, "GET", "/boot/52:54:00:12:34:56", "", "")
	if strings.Contains(body, "wimboot") || !strings.Contains(body, "exit 1") {
		t.Fatalf("deployed although the consumed arm could not be saved:\n%s", body)
	}
	if f.s.st.Armed["52:54:00:12:34:56"] == nil {
		t.Fatal("arm lost in memory after the failed save")
	}
	if code, _ := f.do(t, "POST", "/api/armed", `{"mac":"52:54:00:00:00:02","manifest":"win11-pro"}`, ""); code != 500 || f.s.st.Armed["52:54:00:00:00:02"] != nil {
		t.Fatalf("arm reported or kept although it could not be saved: %d", code)
	}
}

func TestManifestWindowCloses(t *testing.T) {
	f := newFixture(t, "")
	f.arm(t, "52:54:00:12:34:56")
	f.do(t, "GET", "/boot/52:54:00:12:34:56", "", "")
	f.now = f.now.Add(11 * time.Minute)
	if code, _ := f.do(t, "GET", "/manifest/52:54:00:12:34:56", "", ""); code != 404 {
		t.Fatalf("manifest served after its window: %d", code)
	}
}

func TestEmptyKeyNeverAuthenticates(t *testing.T) {
	f := newFixture(t, "")
	f.arm(t, "52:54:00:12:34:56")
	f.do(t, "GET", "/boot/52:54:00:12:34:56", "", "")
	f.s.st.Deployments["52:54:00:12:34:56"].Key = "" // e.g. a record from an older state file
	if code, _ := f.do(t, "POST", "/api/report", `{"key":"","macs":["52:54:00:12:34:56"],"phase":"done","status":"done"}`, ""); code == 204 {
		t.Fatal("empty key accepted")
	}
}

func TestFailedRestoreDegradesTheServer(t *testing.T) {
	f := newFixture(t, "")
	f.arm(t, "52:54:00:12:34:56")
	// Make every save fail: the state "file" is now a directory, so both the save and the restoring save fail.
	f.s.StatePath = t.TempDir()
	if code, body := f.do(t, "POST", "/api/armed", `{"mac":"52:54:00:00:00:02","manifest":"win11-pro"}`, ""); code != 500 || !strings.Contains(body, "degraded") {
		t.Fatalf("arm with an unwritable state: %d %s", code, body)
	}
	if !f.s.degraded {
		t.Fatal("not degraded")
	}
	if _, body := f.do(t, "GET", "/boot/52:54:00:12:34:56", "", ""); strings.Contains(body, "wimboot") {
		t.Fatalf("a degraded server deployed:\n%s", body)
	}
	if _, st := f.do(t, "GET", "/api/state", "", ""); !strings.Contains(st, `"degraded":true`) {
		t.Fatalf("state hides degradation: %s", st)
	}
}

func TestDisarmAfterBootSaysSo(t *testing.T) {
	f := newFixture(t, "")
	f.arm(t, "52:54:00:12:34:56")
	f.do(t, "GET", "/boot/52:54:00:12:34:56", "", "")
	if code, body := f.do(t, "DELETE", "/api/armed/52:54:00:12:34:56", "", ""); code != 404 || !strings.Contains(body, "not cancelled") {
		t.Fatalf("disarm after boot: %d %s", code, body)
	}
}

func TestConsumedArmStaysConsumedAfterRestart(t *testing.T) {
	f := newFixture(t, "")
	f.arm(t, "52:54:00:12:34:56")
	f.do(t, "GET", "/boot/52:54:00:12:34:56", "", "")
	s2 := &Server{StatePath: f.s.StatePath, Now: f.s.Now}
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	if s2.st.Armed["52:54:00:12:34:56"] != nil {
		t.Fatal("consumed arm came back after a restart")
	}
}

func TestArmsDoNotSurviveARestartButDeploymentsDo(t *testing.T) {
	f := newFixture(t, "")
	f.arm(t, "52:54:00:12:34:56")
	f.arm(t, "52:54:00:00:00:09")
	f.do(t, "GET", "/boot/52:54:00:12:34:56", "", "") // this one boots: a deployment record exists
	s2 := &Server{StatePath: f.s.StatePath, Now: f.s.Now}
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	if len(s2.st.Armed) != 0 {
		t.Fatalf("arms came back after a restart: %v", s2.st.Armed)
	}
	if s2.st.Deployments["52:54:00:12:34:56"] == nil {
		t.Fatal("deployment history lost across restart")
	}
}

func TestStaleArmOnDiskAfterDoubleFaultIsDroppedOnRestart(t *testing.T) {
	f := newFixture(t, "")
	// The file holds an arm the operator was told failed (rename succeeded, directory sync did not, restore also failed).
	f.arm(t, "52:54:00:12:34:56")
	f.s.degraded = true // what commit() leaves behind
	s2 := &Server{StatePath: f.s.StatePath, ManifestDir: f.s.ManifestDir, ImageBase: f.s.ImageBase, ArmTTL: time.Hour, ReportTTL: time.Hour, Now: f.s.Now}
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s2.Handler())
	defer ts.Close()
	r, err := http.Get(ts.URL + "/boot/52:54:00:12:34:56")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if strings.Contains(string(b), "wimboot") {
		t.Fatalf("a restarted server deployed a machine from a stale on-disk arm:\n%s", b)
	}
}
