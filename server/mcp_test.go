package main

import (
	"bytes"
	"context"
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

func newMCPFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t, "administrator-test-token")
	f.s.ImageBase = "https://boot.lab.internal/windows11"
	f.s.MCP = &mcpControl{MediaDir: t.TempDir(), ImportDir: t.TempDir(), PXEDir: t.TempDir(), Bound: f.ts.Listener.Addr()}
	f.s.Auth.Path = filepath.Join(t.TempDir(), "accounts.json")
	t.Cleanup(func() {
		f.s.MCP.jobsMu.Lock()
		defer f.s.MCP.jobsMu.Unlock()
		for _, j := range f.s.MCP.jobs {
			j.cancel()
		}
	})
	return f
}
func (f *fixture) rpc(t *testing.T, token, method string, params any) rpcResponse {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 7, "method": method, "params": params})
	if params == nil {
		b, _ = json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 7, "method": method})
	}
	code, body := f.do(t, "POST", "/mcp", string(b), token)
	if code != 200 {
		t.Fatalf("MCP HTTP %d: %s", code, body)
	}
	var response rpcResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatal(err)
	}
	return response
}
func (f *fixture) callTool(t *testing.T, token, name string, args any) toolResult {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	if name == "machine_arm" {
		m := args.(map[string]any)
		if _, ok := m["request_id"]; !ok {
			m["request_id"] = randomID()
		}
	}
	r := f.rpc(t, token, "tools/call", map[string]any{"name": name, "arguments": args})
	if r.Error != nil {
		t.Fatalf("%s: protocol error %+v", name, r.Error)
	}
	b, _ := json.Marshal(r.Result)
	var result toolResult
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func (f *fixture) adminTool(t *testing.T, name string, args any) toolResult {
	return f.callTool(t, f.s.Token, name, args)
}
func requireToolOK(t *testing.T, r toolResult) map[string]any {
	t.Helper()
	if r.IsError {
		t.Fatalf("tool failed: %+v", r.StructuredContent)
	}
	return r.StructuredContent
}
func requireToolCode(t *testing.T, r toolResult, code string) {
	t.Helper()
	e, _ := r.StructuredContent["error"].(map[string]any)
	if !r.IsError || e["code"] != code {
		t.Fatalf("want %s: %+v", code, r)
	}
	if len(e["next_steps"].([]any)) == 0 {
		t.Fatal("missing recovery guidance")
	}
}

func TestMCPTransportAndDiscovery(t *testing.T) {
	f := newMCPFixture(t)
	for _, v := range []string{mcpVersion, "2025-06-18", "2025-03-26", "future-version"} {
		r := f.rpc(t, f.s.Token, "initialize", map[string]any{"protocolVersion": v, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}})
		if r.Error != nil {
			t.Fatal(r.Error)
		}
		m := r.Result.(map[string]any)
		if !supportedMCP(m["protocolVersion"].(string)) {
			t.Fatal(m)
		}
	}
	r := f.rpc(t, f.s.Token, "tools/list", nil)
	tools := r.Result.(map[string]any)["tools"].([]any)
	if len(tools) < 25 {
		t.Fatalf("missing workflow tools: %d", len(tools))
	}
	for _, tool := range tools {
		m := tool.(map[string]any)
		if m["name"] == "machine_arm" {
			if m["annotations"].(map[string]any)["destructiveHint"] != true {
				t.Fatal("arm must be labelled destructive")
			}
		}
	}
	for _, method := range []string{"resources/list", "resources/templates/list", "prompts/list", "ping"} {
		if r := f.rpc(t, f.s.Token, method, nil); r.Error != nil {
			t.Fatal(r.Error)
		}
	}
	for _, resource := range mcpResources() {
		r := f.rpc(t, f.s.Token, "resources/read", map[string]any{"uri": resource["uri"]})
		if r.Error != nil {
			t.Fatal(r.Error)
		}
	}
	if r := f.rpc(t, f.s.Token, "prompts/get", map[string]any{"name": "guided_deployment"}); r.Error != nil {
		t.Fatal(r.Error)
	}
	if r := f.rpc(t, f.s.Token, "not/a/method", nil); r.Error == nil || r.Error.Code != -32601 {
		t.Fatal(r)
	}
	if r := f.rpc(t, f.s.Token, "tools/call", map[string]any{"name": "machine_arm", "arguments": map[string]any{"mac": "x", "manifest": "win11-pro", "unexpected": true}}); r.Error == nil || r.Error.Code != -32602 {
		t.Fatal(r)
	}
	if r := f.rpc(t, f.s.Token, "tools/call", map[string]any{"name": "whoami", "arguments": nil}); r.Error == nil || r.Error.Code != -32602 {
		t.Fatal("null arguments accepted")
	}
	for _, body := range []string{`{`, `[]`, `null`, `{"jsonrpc":"1.0","id":1,"method":"ping"}`} {
		code, out := f.do(t, "POST", "/mcp", body, f.s.Token)
		if code != 200 || !strings.Contains(out, `"error"`) {
			t.Fatalf("invalid request accepted: %d %s", code, out)
		}
	}
	if code, body := f.do(t, "POST", "/mcp", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, f.s.Token); code != 202 || body != "" {
		t.Fatalf("notification: %d %s", code, body)
	}
	if code, _ := f.do(t, "GET", "/mcp", "", f.s.Token); code != 405 {
		t.Fatalf("GET: %d", code)
	}
	if code, _ := f.do(t, "POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "wrong"); code != 401 {
		t.Fatalf("bad auth: %d", code)
	}
	if code, _ := f.req(t, "POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "", map[string]string{"Authorization": "Bearer " + f.s.Token, "Origin": evil, "Content-Type": "application/json"}); code != 403 {
		t.Fatalf("cross origin: %d", code)
	}
	if code, _ := f.req(t, "POST", "/mcp", `{}`, "", map[string]string{"Authorization": "Bearer " + f.s.Token, "MCP-Protocol-Version": "unknown", "Content-Type": "application/json"}); code != 400 {
		t.Fatalf("bad protocol header: %d", code)
	}
	plain := newFixture(t, "")
	if code, _ := plain.do(t, "POST", "/mcp", `{}`, ""); code != 404 {
		t.Fatal("MCP should be opt in")
	}
}

func TestMCPArmMonitorDisarmUsesExistingLifecycle(t *testing.T) {
	f := newMCPFixture(t)
	mac := "52:54:00:00:00:21"
	requireToolOK(t, f.adminTool(t, "machine_arm", map[string]any{"mac": mac, "manifest": "win11-pro"}))
	pending := requireToolOK(t, f.adminTool(t, "deployment_get", map[string]any{"mac": mac}))
	if pending["armed"] == nil || pending["terminal"] != false {
		t.Fatal(pending)
	}
	// Inspection and preflight must not consume the one-time boot ticket.
	requireToolOK(t, f.adminTool(t, "profiles_list", nil))
	requireToolOK(t, f.adminTool(t, "profile_get", map[string]any{"name": "win11-pro"}))
	requireToolOK(t, f.adminTool(t, "profile_validate", map[string]any{"name": "win11-pro"}))
	if f.s.st.Armed[mac] == nil {
		t.Fatal("inspection consumed the arm")
	}
	key := f.bootAndKey(t, mac)
	live := requireToolOK(t, f.adminTool(t, "deployment_get", map[string]any{"mac": mac}))
	if live["terminal"] != false || live["deployment"].(map[string]any)["status"] != "booting" {
		t.Fatal(live)
	}
	encoded, _ := json.Marshal(live)
	if strings.Contains(string(encoded), key) || strings.Contains(string(encoded), "ticket") {
		t.Fatal("capability leaked")
	}
	requireToolCode(t, f.adminTool(t, "machine_disarm", map[string]any{"mac": mac}), "not_armed")
	f.now = f.now.Add(3 * time.Minute)
	if r := requireToolOK(t, f.adminTool(t, "deployment_get", map[string]any{"mac": mac})); r["stale"] != true || r["terminal"] != false {
		t.Fatal(r)
	}
	report, _ := json.Marshal(map[string]any{"key": key, "macs": []string{mac}, "phase": "download", "status": "failed", "message": "image SHA-256 mismatch"})
	if code, body := f.do(t, "POST", "/api/report", string(report), ""); code != 204 {
		t.Fatalf("report: %d %s", code, body)
	}
	done := requireToolOK(t, f.adminTool(t, "deployment_get", map[string]any{"mac": mac}))
	if done["terminal"] != true || !strings.Contains(done["next_steps"].([]any)[0].(string), "hash") {
		t.Fatal(done)
	}
	if script := func() string { _, s := f.boot(t, mac); return s }(); strings.Contains(script, "wimboot") {
		t.Fatal("second boot reimaged")
	}
}

func TestMCPProfileConcurrencyValidationAndConfinement(t *testing.T) {
	f := newMCPFixture(t)
	var profile map[string]any
	_ = json.Unmarshal([]byte(testProfile), &profile)
	r := requireToolOK(t, f.adminTool(t, "profile_put", map[string]any{"name": "new-image", "profile": profile}))
	hash := r["sha256"]
	requireToolCode(t, f.adminTool(t, "profile_put", map[string]any{"name": "new-image", "profile": profile}), "profile_conflict")
	requireToolOK(t, f.adminTool(t, "profile_put", map[string]any{"name": "new-image", "profile": profile, "expected_sha256": hash}))
	requireToolCode(t, f.adminTool(t, "profile_put", map[string]any{"name": "../escape", "profile": profile}), "unknown_profile")
	requireToolOK(t, f.adminTool(t, "machine_arm", map[string]any{"mac": "52:54:00:00:00:22", "manifest": "new-image"}))
	requireToolCode(t, f.adminTool(t, "profile_delete", map[string]any{"name": "new-image", "expected_sha256": hash}), "profile_in_use")
	requireToolCode(t, f.adminTool(t, "server_configure", map[string]any{"arm_ttl_seconds": 600}), "host_busy")
	requireToolOK(t, f.adminTool(t, "machine_disarm", map[string]any{"mac": "52:54:00:00:00:22"}))
	requireToolOK(t, f.adminTool(t, "profile_delete", map[string]any{"name": "new-image", "expected_sha256": hash}))
	for _, i := range []string{"windows", "debian", "ubuntu", "proxmox", "bazzite"} {
		if _, err := parseProfile(mustJSON(t, profileTemplate(i)), i); err == nil {
			t.Fatalf("placeholder template %s should fail closed", i)
		}
		g := requireToolOK(t, f.adminTool(t, "setup_guide", map[string]any{"installer": i}))
		if len(g["guide"].(string)) < 500 {
			t.Fatal("missing guide")
		}
	}
}
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func waitMediaJob(t *testing.T, f *fixture, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r := requireToolOK(t, f.adminTool(t, "job_get", map[string]any{"job_id": id}))
		if r["terminal"] == true {
			return r
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("media job did not finish")
	return nil
}
func TestMCPMediaImportHashMismatchAndNoOverwrite(t *testing.T) {
	f := newMCPFixture(t)
	data := []byte("verified fixture media")
	if err := os.WriteFile(filepath.Join(f.s.MCP.ImportDir, "kernel"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	start := requireToolOK(t, f.adminTool(t, "media_import", map[string]any{"source": "kernel", "area": "media", "file": "debian-linux", "sha256": hashBytes(data)}))
	r := waitMediaJob(t, f, start["job_id"].(string))
	if r["status"] != "done" || r["sha256"] != hashBytes(data) || r["pin_verified"] != true {
		t.Fatal(r)
	}
	requireToolCode(t, f.adminTool(t, "media_import", map[string]any{"source": "kernel", "area": "media", "file": "debian-linux"}), "media_exists")
	requireToolCode(t, f.adminTool(t, "media_import", map[string]any{"source": "../kernel", "area": "media", "file": "escape"}), "file_error")
	start = requireToolOK(t, f.adminTool(t, "media_import", map[string]any{"source": "kernel", "area": "media", "file": "bad-pin", "sha256": strings.Repeat("a", 64)}))
	r = waitMediaJob(t, f, start["job_id"].(string))
	if r["status"] != "failed" {
		t.Fatal(r)
	}
	if _, err := os.Stat(filepath.Join(f.s.MCP.MediaDir, "bad-pin")); !os.IsNotExist(err) {
		t.Fatal("bad hash was published")
	}
	entries, _ := os.ReadDir(f.s.MCP.MediaDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "m2w-part-") {
			t.Fatal("unpublished partial remains")
		}
	}
	start = requireToolOK(t, f.adminTool(t, "media_inspect", map[string]any{"area": "media", "file": "debian-linux"}))
	r = waitMediaJob(t, f, start["job_id"].(string))
	if r["status"] != "done" || r["pin_verified"] != false {
		t.Fatal(r)
	}
	requireToolCode(t, f.adminTool(t, "media_fetch", map[string]any{"area": "media", "file": "test", "url": "http://localhost/file", "sha256": strings.Repeat("a", 64), "max_bytes": 100}), "invalid_url")
	root, err := os.OpenRoot(f.s.MCP.MediaDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	source, err := os.Open(filepath.Join(f.s.MCP.ImportDir, "kernel"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	j := &mediaJob{Operation: "media_import", File: "cancelled"}
	if err := runMediaJob(ctx, j, root, source, "", "", 1024, nil); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := root.Stat("cancelled"); !os.IsNotExist(err) {
		t.Fatal("cancelled job published")
	}
}

func TestMCPStdioBridgeKeepsOneHostAndNoLogsOnStdout(t *testing.T) {
	f := newMCPFixture(t)
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}` + "\n" + `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami","arguments":{}}}` + "\n"
	var out bytes.Buffer
	if err := runMCPBridge(context.Background(), strings.NewReader(input), &out, f.ts.URL+"/mcp", f.s.Token); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdout is not two responses: %s", out.String())
	}
	for _, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Fatal(line)
		}
	}
	if !strings.Contains(lines[1], "server-admin") {
		t.Fatal(lines[1])
	}
	if err := runMCPBridge(context.Background(), strings.NewReader(""), io.Discard, "http://192.0.2.1:8080/mcp", f.s.Token); err == nil {
		t.Fatal("remote HTTP bridge should require HTTPS")
	}
	if err := runMCPBridge(context.Background(), strings.NewReader(""), io.Discard, f.ts.URL+"/mcp", ""); err == nil {
		t.Fatal("missing token accepted")
	}
}

func TestMCPHostedMediaErrorsAreActionableAndBounded(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer source.Close()
	check := probeMedia(context.Background(), source.URL+"/missing?secret=signed-query")
	if check["ok"] != false || check["http_status"] != 403 || strings.Contains(check["url"].(string), "secret") {
		t.Fatal(check)
	}
	if err := safeMediaRedirect(httptest.NewRequest("GET", "http://insecure/media", nil), nil); err == nil {
		t.Fatal("redirect downgrade accepted")
	}
}

func TestMCPArmRequestRetryCannotScheduleAnotherWipe(t *testing.T) {
	f := newMCPFixture(t)
	args := map[string]any{"mac": "52:54:00:00:00:61", "manifest": "win11-pro", "request_id": "operator-request-61"}
	first := requireToolOK(t, f.adminTool(t, "machine_arm", args))
	runID := first["run_id"]
	if runID == "" || first["replayed"] != false {
		t.Fatal(first)
	}
	if r := requireToolOK(t, f.adminTool(t, "machine_arm", args)); r["run_id"] != runID || r["replayed"] != true {
		t.Fatal(r)
	}
	f.bootAndKey(t, args["mac"].(string))
	if r := requireToolOK(t, f.adminTool(t, "machine_arm", args)); r["replayed"] != true {
		t.Fatal(r)
	}
	if len(f.s.st.Armed) != 0 {
		t.Fatal("retry after consumption rearmed target")
	}
	if err := f.s.Load(); err != nil {
		t.Fatal(err)
	}
	if r := requireToolOK(t, f.adminTool(t, "machine_arm", args)); r["run_id"] != runID || r["replayed"] != true {
		t.Fatal(r)
	}
	if len(f.s.st.Armed) != 0 {
		t.Fatal("retry after restart rearmed target")
	}
	args["mac"] = "52:54:00:00:00:62"
	requireToolCode(t, f.adminTool(t, "machine_arm", args), "request_conflict")
}

func TestMCPStoppedTargetRecoveryAndSettingsPersistence(t *testing.T) {
	f := newMCPFixture(t)
	mac := "52:54:00:00:00:91"
	requireToolOK(t, f.adminTool(t, "machine_arm", map[string]any{"mac": mac, "manifest": "win11-pro", "request_id": "recovery-91"}))
	key := f.bootAndKey(t, mac)
	requireToolCode(t, f.adminTool(t, "deployment_abandon", map[string]any{"mac": mac, "target_stopped": false, "reason": "fixture"}), "target_not_stopped")
	requireToolCode(t, f.adminTool(t, "server_configure", map[string]any{"arm_ttl_seconds": 600}), "host_busy")
	requireToolOK(t, f.adminTool(t, "deployment_abandon", map[string]any{"mac": mac, "target_stopped": true, "reason": "Fixture target was stopped externally"}))
	closed := requireToolOK(t, f.adminTool(t, "deployment_get", map[string]any{"mac": mac}))
	if closed["terminal"] != true || closed["outcome_unknown"] != true {
		t.Fatal(closed)
	}
	if code, _ := f.do(t, "POST", "/api/report", string(mustJSON(t, map[string]any{"key": key, "macs": []string{mac}, "status": "done"})), ""); code != 404 {
		t.Fatal("abandoned target retained report capability")
	}
	f.s.MCP.SettingsPath = filepath.Join(t.TempDir(), "mcp-settings.json")
	requireToolOK(t, f.adminTool(t, "server_configure", map[string]any{"arm_ttl_seconds": 600, "use_media_for_linux": true}))
	settings, err := readMCPSettings(f.s.MCP.SettingsPath)
	if err != nil || settings.ArmTTLSeconds != 600 || settings.LinuxAssets != f.s.MCP.MediaDir {
		t.Fatalf("%+v %v", settings, err)
	}
	f.s.MCP.SettingsPath = filepath.Join(t.TempDir(), "missing", "mcp-settings.json")
	requireToolCode(t, f.adminTool(t, "server_configure", map[string]any{"arm_ttl_seconds": 900}), "config_save_failed")
	if f.s.ArmTTL != 10*time.Minute {
		t.Fatal("failed persistence changed running configuration")
	}
	if code, body := f.do(t, "GET", "/api/status", "", ""); code != 200 || strings.Contains(body, mac) || strings.Contains(body, key) || strings.Contains(body, "manifest") {
		t.Fatalf("public status leaked target data: %d %s", code, body)
	}
}

func TestMCPHTTPSMediaFetchPinSizeAndJobRecovery(t *testing.T) {
	payload := []byte("fixture boot media over TLS")
	var source *httptest.Server
	source = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("operator credential forwarded to media host")
		}
		switch r.URL.Path {
		case "/forbidden":
			w.WriteHeader(403)
		case "/redirect":
			http.Redirect(w, r, "http://127.0.0.1/not-allowed", 302)
		default:
			_, _ = w.Write(payload)
		}
	}))
	defer source.Close()
	f := newMCPFixture(t)
	f.s.MCP.mediaTransport = source.Client().Transport
	for _, tc := range []struct {
		file, path, pin, status string
		limit                   int64
	}{{"good", "/good", hashBytes(payload), "done", 1024}, {"bad-hash", "/good", strings.Repeat("a", 64), "failed", 1024}, {"too-large", "/good", hashBytes(payload), "failed", 1}, {"forbidden", "/forbidden", hashBytes(payload), "failed", 1024}, {"downgrade", "/redirect", hashBytes(payload), "failed", 1024}} {
		start := requireToolOK(t, f.adminTool(t, "media_fetch", map[string]any{"area": "media", "file": tc.file, "url": source.URL + tc.path, "sha256": tc.pin, "max_bytes": tc.limit}))
		result := waitMediaJob(t, f, start["job_id"].(string))
		if result["status"] != tc.status || result["success"] != (tc.status == "done") {
			t.Fatal(result)
		}
		_, err := os.Stat(filepath.Join(f.s.MCP.MediaDir, tc.file))
		if tc.status == "done" {
			if err != nil || result["pin_verified"] != true {
				t.Fatal(result, err)
			}
		} else if !os.IsNotExist(err) {
			t.Fatal("failed download published media")
		}
		jobs := requireToolOK(t, f.adminTool(t, "jobs_list", nil))["jobs"].([]any)
		found := false
		for _, job := range jobs {
			if job.(map[string]any)["job_id"] == start["job_id"] {
				found = true
			}
		}
		if !found {
			t.Fatal("lost job ID cannot be recovered")
		}
	}
}

func TestMCPOverlappingProfileRootCannotExposeOperatorData(t *testing.T) {
	f := newMCPFixture(t)
	requireToolOK(t, f.adminTool(t, "machine_arm", map[string]any{"mac": "52:54:00:00:00:92", "manifest": "win11-pro"}))
	f.bootAndKey(t, "52:54:00:00:00:92") // state.json now contains a real report key
	f.s.Auth.Path = filepath.Join(f.s.ManifestDir, "accounts.json")
	requireToolOK(t, f.adminTool(t, "account_put", map[string]any{"username": "test-viewer", "role": "viewer", "profiles": []string{"*"}, "password": "synthetic-local-test-passphrase"}))
	for _, name := range []string{"state", "accounts"} {
		requireToolCode(t, f.adminTool(t, "profile_get", map[string]any{"name": name}), "protected_file")
		requireToolCode(t, f.adminTool(t, "profile_delete", map[string]any{"name": name, "expected_sha256": strings.Repeat("a", 64)}), "protected_file")
	}
	profiles := requireToolOK(t, f.adminTool(t, "profiles_list", nil))["profiles"].([]any)
	for _, row := range profiles {
		n := row.(map[string]any)["name"]
		if n == "state" || n == "accounts" {
			t.Fatal("operator data was listed as a profile")
		}
	}
}

func TestMCPJSONSchemaIntegerEncoding(t *testing.T) {
	f := newMCPFixture(t)
	code, body := f.do(t, "POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"server_configure","arguments":{"arm_ttl_seconds":600.0}}}`, f.s.Token)
	var response struct {
		Result toolResult `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatal(err)
	}
	if code != 200 || response.Result.IsError || f.s.ArmTTL != 10*time.Minute {
		t.Fatalf("JSON Schema integer rejected: %d %s", code, body)
	}
}
