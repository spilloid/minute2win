package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (f *fixture) createAccountToken(t *testing.T, user, role string, profiles []string) string {
	t.Helper()
	requireToolOK(t, f.adminTool(t, "account_put", map[string]any{"username": user, "role": role, "profiles": profiles, "password": "a unique local test passphrase"}))
	r := requireToolOK(t, f.adminTool(t, "account_token_create", map[string]any{"username": user}))
	return r["token"].(string)
}
func TestRBACEnforcesImagesAcrossMCPAPIAndUI(t *testing.T) {
	f := newMCPFixture(t)
	operator := f.createAccountToken(t, "imager", "operator", []string{"win11-pro"})
	viewer := f.createAccountToken(t, "observer", "viewer", []string{"other"})
	if err := os.WriteFile(filepath.Join(f.s.ManifestDir, "other.json"), []byte(testProfile), 0o600); err != nil {
		t.Fatal(err)
	}
	requireToolOK(t, f.callTool(t, operator, "machine_arm", map[string]any{"mac": "52:54:00:00:00:31", "manifest": "win11-pro"}))
	requireToolCode(t, f.callTool(t, operator, "machine_arm", map[string]any{"mac": "52:54:00:00:00:32", "manifest": "other"}), "forbidden")
	requireToolCode(t, f.callTool(t, operator, "server_configure", map[string]any{"image_base": ""}), "forbidden")
	requireToolCode(t, f.callTool(t, viewer, "machine_arm", map[string]any{"mac": "52:54:00:00:00:32", "manifest": "other"}), "forbidden")
	requireToolCode(t, f.callTool(t, operator, "profile_get", map[string]any{"name": "other"}), "forbidden")
	requireToolOK(t, f.adminTool(t, "machine_arm", map[string]any{"mac": "52:54:00:00:00:32", "manifest": "other"}))
	if code, _ := f.do(t, "POST", "/api/armed", `{"mac":"52:54:00:00:00:33","manifest":"other"}`, operator); code != 403 {
		t.Fatalf("API escaped image scope: %d", code)
	}
	if code, _ := f.do(t, "DELETE", "/api/armed/52:54:00:00:00:32", "", operator); code != 403 {
		t.Fatalf("API scoped disarm should refuse: %d", code)
	}
	if code, _ := f.do(t, "DELETE", "/api/armed/52:54:00:00:00:32", "", viewer); code != 403 {
		t.Fatalf("viewer disarm: %d", code)
	}
	for _, path := range []string{"/api/state", "/api/manifests", "/ui/armed", "/"} {
		code, body := f.do(t, "GET", path, "", operator)
		if code != 200 {
			t.Fatalf("%s: %d %s", path, code, body)
		}
		if strings.Contains(body, `"other"`) || strings.Contains(body, ">other<") || strings.Contains(body, "52:54:00:00:00:32") {
			t.Fatalf("scope leak at %s: %s", path, body)
		}
	}
	if code, _ := f.do(t, "GET", "/api/state", "", ""); code != 401 {
		t.Fatalf("private state should require login: %d", code)
	}
	if code, body := f.do(t, "GET", "/healthz", "", ""); code != 200 || strings.Contains(body, "mac") {
		t.Fatalf("health: %d %s", code, body)
	}
	if f.s.st.Armed["52:54:00:00:00:32"] == nil {
		t.Fatal("out-of-scope arm was removed")
	}
}

func TestLocalSignInOnceThenArmMultipleMachinesAndLogout(t *testing.T) {
	f := newMCPFixture(t)
	token := f.createAccountToken(t, "localoperator", "operator", []string{"win11-pro"})
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	res, err := client.PostForm(f.ts.URL+"/login", url.Values{"username": {"localoperator"}, "password": {"a unique local test passphrase"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || strings.Contains(string(body), `name="token"`) || !strings.Contains(string(body), "localoperator") {
		t.Fatalf("login: %d %s", res.StatusCode, body)
	}
	for _, mac := range []string{"52:54:00:00:00:41", "52:54:00:00:00:42"} {
		res, err := client.PostForm(f.ts.URL+"/ui/arm", url.Values{"mac": {mac}, "manifest": {"win11-pro"}})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if !strings.Contains(string(b), `class="ok"`) {
			t.Fatalf("per-device credential still required: %s", b)
		}
	}
	u, _ := url.Parse(f.ts.URL)
	cookies := jar.Cookies(u)
	if len(cookies) == 0 {
		t.Fatal("no session cookie")
	}
	// A cookie must not allow a bogus bearer to authenticate MCP.
	req, _ := http.NewRequest("POST", f.ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer bogus")
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("MCP accepted browser cookie instead of bearer")
	}
	res, err = client.PostForm(f.ts.URL+"/logout", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	res, err = client.PostForm(f.ts.URL+"/ui/arm", url.Values{"mac": {"52:54:00:00:00:43"}, "manifest": {"win11-pro"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("logout did not revoke session")
	}
	// Changing role/scope/password revokes both old API credentials and sessions.
	requireToolOK(t, f.adminTool(t, "account_put", map[string]any{"username": "localoperator", "role": "viewer", "profiles": []string{"win11-pro"}}))
	if code, _ := f.do(t, "POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, token); code != 401 {
		t.Fatal("old account token survived policy update")
	}
	data, err := os.ReadFile(f.s.Auth.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), token) || strings.Contains(string(data), "a unique local test passphrase") {
		t.Fatal("raw credential persisted")
	}
	loaded := newAuthStore(f.s.Auth.Path)
	if err := loaded.load(); err != nil {
		t.Fatal(err)
	}
	if loaded.users["localoperator"].Role != "viewer" {
		t.Fatal("account policy was not persisted")
	}
}

func TestBootstrapSessionCookieAndCrossSiteLogin(t *testing.T) {
	f := newMCPFixture(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.PostForm(f.ts.URL+"/login", url.Values{"username": {"server-admin"}, "password": {f.s.Token}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 303 {
		t.Fatal(res.StatusCode)
	}
	cookies := res.Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Value == f.s.Token || cookies[0].MaxAge != 43200 {
		t.Fatalf("unsafe cookie: %+v", cookies)
	}
	if code, _ := f.req(t, "POST", "/login", url.Values{"username": {"server-admin"}, "password": {f.s.Token}}.Encode(), "", map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": evil}); code != 403 {
		t.Fatal("cross-site login allowed")
	}
	if code, _ := f.req(t, "POST", "/logout", "", "", map[string]string{"Origin": evil}); code != 403 {
		t.Fatal("cross-site logout allowed")
	}
}

func TestProxySSORequiresSecretAndProvisionedIdentity(t *testing.T) {
	f := newMCPFixture(t)
	f.createAccountToken(t, "sso-user", "operator", []string{"win11-pro"})
	f.s.Auth.ProxyHeader = "X-authentik-username"
	f.s.Auth.ProxySecret = strings.Repeat("p", 32)
	for _, headers := range []map[string]string{{"X-authentik-username": "sso-user"}, {"X-authentik-username": "sso-user", "X-M2W-Proxy-Secret": "wrong"}, {"X-authentik-username": "unknown", "X-M2W-Proxy-Secret": strings.Repeat("p", 32)}} {
		if code, _ := f.req(t, "GET", "/api/state", "", "", headers); code != 401 {
			t.Fatal("spoofed or unknown proxy identity accepted")
		}
	}
	if code, _ := f.req(t, "GET", "/api/state", "", "", map[string]string{"X-authentik-username": "sso-user", "X-M2W-Proxy-Secret": strings.Repeat("p", 32)}); code != 200 {
		t.Fatal(code)
	}
	if code, _ := f.req(t, "POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "", map[string]string{"X-authentik-username": "sso-user", "X-M2W-Proxy-Secret": strings.Repeat("p", 32), "Authorization": "Bearer bogus", "Content-Type": "application/json"}); code != 401 {
		t.Fatal("proxy header bypassed MCP bearer")
	}
}

func TestAccountFailureDoesNotChangeMemoryOrExposeHashes(t *testing.T) {
	f := newMCPFixture(t)
	token := f.createAccountToken(t, "keepme", "operator", []string{"win11-pro"})
	f.s.Auth.Path = filepath.Join(t.TempDir(), "missing", "accounts.json")
	requireToolCode(t, f.adminTool(t, "account_put", map[string]any{"username": "keepme", "role": "admin", "profiles": []string{"*"}}), "file_error")
	r := f.callTool(t, token, "whoami", nil)
	if r.IsError || r.StructuredContent["identity"].(map[string]any)["role"] != "operator" {
		t.Fatal("failed account save changed active policy")
	}
	list := requireToolOK(t, f.adminTool(t, "accounts_list", nil))
	b, _ := json.Marshal(list)
	if strings.Contains(string(b), "password_hash") || strings.Contains(string(b), "token_hashes") {
		t.Fatal("credential hashes exposed")
	}
}
