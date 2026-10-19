package main

// Arm hardening (2026-10-19): the three gaps a live evaluation confirmed, each reproduced here against the HTTP handler or the
// real binary before it was fixed.
//   1. Any web page the operator visits could arm a wipe (cross-site POSTs to /api/armed and /ui/arm were accepted).
//   2. With no M2W_TOKEN the server listened on every interface by default, so anyone on the LAN could arm a wipe.
//   3. -image-base (and the shipped profile's image URL) defaulted to the maintainer's own host.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// req sends a request with exactly the headers given (browser headers such as Origin and Sec-Fetch-Site included), optionally
// addressed to another Host, the way a browser on a DNS-rebound name would send it.
func (f *fixture) req(t *testing.T, method, path, body, host string, hdr map[string]string) (int, string) {
	t.Helper()
	r, _ := http.NewRequest(method, f.ts.URL+path, strings.NewReader(body))
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	if host != "" {
		r.Host = host
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

const evil = "https://attacker.example"

// Gap 1, as found: a page on another site posts text/plain JSON (a "simple" request, so no CORS preflight) to /api/armed, and an
// HTML form to /ui/arm. Both armed the MAC.
func TestCrossSiteArmIsRefused(t *testing.T) {
	f := newFixture(t, "")
	body := `{"mac":"52:54:00:12:34:56","manifest":"win11-pro"}`
	cases := []struct {
		name, method, path, body string
		hdr                      map[string]string
	}{
		{"modern browser, text/plain JSON", "POST", "/api/armed", body,
			map[string]string{"Content-Type": "text/plain", "Origin": evil, "Sec-Fetch-Site": "cross-site"}},
		{"older browser (Origin only), text/plain JSON", "POST", "/api/armed", body,
			map[string]string{"Content-Type": "text/plain", "Origin": evil}},
		{"modern browser, JSON content type", "POST", "/api/armed", body,
			map[string]string{"Content-Type": "application/json", "Origin": evil, "Sec-Fetch-Site": "cross-site"}},
		{"another site on the same host (same-site, other port)", "POST", "/api/armed", body,
			map[string]string{"Content-Type": "application/json", "Origin": "http://127.0.0.1:1", "Sec-Fetch-Site": "same-site"}},
		{"form post to the UI", "POST", "/ui/arm", url.Values{"mac": {"52:54:00:12:34:56"}, "manifest": {"win11-pro"}}.Encode(),
			map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": evil, "Sec-Fetch-Site": "cross-site"}},
		{"form post to the UI, Origin only", "POST", "/ui/arm", url.Values{"mac": {"52:54:00:12:34:56"}, "manifest": {"win11-pro"}}.Encode(),
			map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": evil}},
	}
	for _, c := range cases {
		code, out := f.req(t, c.method, c.path, c.body, "", c.hdr)
		if len(f.s.st.Armed) != 0 {
			t.Fatalf("%s: armed by a cross-site request (%d %s)", c.name, code, out)
		}
		if code != http.StatusForbidden {
			t.Errorf("%s: want 403, got %d %s", c.name, code, out)
		}
	}
}

// Disarming is a state change too: a cross-site page must not be able to cancel an arm (or, through the UI, probe for one).
func TestCrossSiteDisarmIsRefused(t *testing.T) {
	f := newFixture(t, "")
	f.arm(t, "52:54:00:12:34:56")
	f.req(t, "DELETE", "/api/armed/52:54:00:12:34:56", "", "", map[string]string{"Origin": evil, "Sec-Fetch-Site": "cross-site"})
	f.req(t, "POST", "/ui/disarm", "mac=52:54:00:12:34:56", "",
		map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": evil, "Sec-Fetch-Site": "cross-site"})
	if f.s.st.Armed["52:54:00:12:34:56"] == nil {
		t.Fatal("a cross-site request disarmed a machine")
	}
}

// The legitimate callers keep working: the console's own page (same-origin), curl or a script (no browser headers), and the
// WinPE engine's report (Windows PowerShell 5.1 HttpWebRequest: POST, application/json, no Origin).
func TestLegitimateCallersStillWork(t *testing.T) {
	f := newFixture(t, "")
	same := map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": f.ts.URL, "Sec-Fetch-Site": "same-origin"}
	if _, out := f.req(t, "POST", "/ui/arm", "mac=52:54:00:00:00:01&manifest=win11-pro", "", same); !strings.Contains(out, `class="ok"`) {
		t.Fatalf("the console's own form was refused: %s", out)
	}
	if _, out := f.req(t, "POST", "/ui/disarm", "mac=52:54:00:00:00:01", "", same); !strings.Contains(out, "Disarmed") {
		t.Fatalf("the console's own disarm was refused: %s", out)
	}
	// An older browser without Sec-Fetch-Site still sends Origin; it matches the Host.
	if _, out := f.req(t, "POST", "/ui/arm", "mac=52:54:00:00:00:02&manifest=win11-pro", "",
		map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": f.ts.URL}); !strings.Contains(out, `class="ok"`) {
		t.Fatalf("same-origin form from an older browser refused: %s", out)
	}
	if code, out := f.req(t, "POST", "/api/armed", `{"mac":"52:54:00:00:00:03","manifest":"win11-pro"}`, "",
		map[string]string{"Content-Type": "application/json"}); code != 200 {
		t.Fatalf("curl-style arm refused: %d %s", code, out)
	}
	if code, out := f.req(t, "DELETE", "/api/armed/52:54:00:00:00:03", "", "", nil); code != 204 {
		t.Fatalf("curl-style disarm refused: %d %s", code, out)
	}
	key := f.bootAndKey(t, "52:54:00:00:00:02")
	rep := `{"key":"` + key + `","macs":["52:54:00:00:00:02"],"phase":"ready","status":"running"}`
	if code, out := f.req(t, "POST", "/api/report", rep, "", map[string]string{"Content-Type": "application/json"}); code != 204 {
		t.Fatalf("the engine's report was refused: %d %s", code, out)
	}
}

// JSON mutations need a JSON content type: no browser can send that cross-site without a CORS preflight, which this server never
// grants. This also covers a browser too old to send Origin or Sec-Fetch-Site.
func TestJSONMutationsRequireJSONContentType(t *testing.T) {
	f := newFixture(t, "")
	arm := `{"mac":"52:54:00:12:34:56","manifest":"win11-pro"}`
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", "application/jsonx"} {
		hdr := map[string]string{}
		if ct != "" {
			hdr["Content-Type"] = ct
		}
		if code, out := f.req(t, "POST", "/api/armed", arm, "", hdr); code != http.StatusUnsupportedMediaType || len(f.s.st.Armed) != 0 {
			t.Fatalf("Content-Type %q: want 415 and nothing armed, got %d %s (armed %d)", ct, code, out, len(f.s.st.Armed))
		}
	}
	if code, out := f.req(t, "POST", "/api/armed", arm, "", map[string]string{"Content-Type": "application/json; charset=utf-8"}); code != 200 {
		t.Fatalf("application/json with a charset refused: %d %s", code, out)
	}
	key := f.bootAndKey(t, "52:54:00:12:34:56")
	rep := `{"key":"` + key + `","macs":["52:54:00:12:34:56"],"phase":"ready","status":"running"}`
	if code, _ := f.req(t, "POST", "/api/report", rep, "", map[string]string{"Content-Type": "text/plain"}); code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain report: want 415, got %d", code)
	}
}

// With no token, the next browser attack after CSRF is DNS rebinding: the attacker's name resolves to this server, so the browser
// calls the request same-origin. The Host header still names the attacker's domain.
func TestNoTokenRefusesRebindingHost(t *testing.T) {
	f := newFixture(t, "")
	host := "rebind.attacker.example:8080"
	hdr := map[string]string{"Content-Type": "application/json", "Origin": "http://" + host, "Sec-Fetch-Site": "same-origin"}
	code, out := f.req(t, "POST", "/api/armed", `{"mac":"52:54:00:12:34:56","manifest":"win11-pro"}`, host, hdr)
	if len(f.s.st.Armed) != 0 || code != http.StatusForbidden || !strings.Contains(out, "M2W_TOKEN") {
		t.Fatalf("arm through a DNS name with no token: %d %s (armed %d)", code, out, len(f.s.st.Armed))
	}
	form := map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": "http://" + host, "Sec-Fetch-Site": "same-origin"}
	if _, out := f.req(t, "POST", "/ui/arm", "mac=52:54:00:12:34:56&manifest=win11-pro", host, form); len(f.s.st.Armed) != 0 || !strings.Contains(out, "M2W_TOKEN") {
		t.Fatalf("UI arm through a DNS name with no token: %s", out)
	}
	// localhost and IP literals are fine without a token (a rebinding attack needs a name the attacker controls)...
	for _, h := range []string{"localhost:8080", "127.0.0.1:8080", "[::1]:8080", "192.168.1.50:8080", "LOCALHOST"} {
		hdr := map[string]string{"Content-Type": "application/json"}
		if code, out := f.req(t, "POST", "/api/armed", `{"mac":"52:54:00:12:34:56","manifest":"win11-pro"}`, h, hdr); code != 200 {
			t.Fatalf("Host %s refused: %d %s", h, code, out)
		}
	}
	// ...and with a token a host name is fine: the attacker can't supply the token.
	g := newFixture(t, "s3cret")
	hdr["Authorization"] = "Bearer s3cret"
	if code, out := g.req(t, "POST", "/api/armed", `{"mac":"52:54:00:12:34:56","manifest":"win11-pro"}`, "deploy.lab.internal:8080", hdr); code != 200 {
		t.Fatalf("token + host name refused: %d %s", code, out)
	}
}

// Gap 3, at arm time: no -image-base means nothing can be armed (before: the maintainer's host was used silently).
func TestArmRefusedWithoutImageBase(t *testing.T) {
	f := newFixture(t, "")
	f.s.ImageBase = ""
	code, out := f.do(t, "POST", "/api/armed", `{"mac":"52:54:00:12:34:56","manifest":"win11-pro"}`, "")
	if code != 400 || !strings.Contains(out, "-image-base") || len(f.s.st.Armed) != 0 {
		t.Fatalf("armed with no image base: %d %s", code, out)
	}
	if out := f.form(t, "/ui/arm", url.Values{"mac": {"52:54:00:12:34:56"}, "manifest": {"win11-pro"}}); !strings.Contains(out, "-image-base") {
		t.Fatalf("UI did not say why: %s", out)
	}
}

// Gap 3, the profile: a profile still carrying a placeholder (or no) image URL or hash is refused at arm time with the reason,
// instead of booting WinPE and failing there after the arm was consumed.
func TestArmRefusesProfilesThatAreNotReady(t *testing.T) {
	f := newFixture(t, "")
	h := strings.Repeat("ab", 32)
	bad := map[string]string{
		"placeholder-host": `{"schemaVersion":1,"image":{"source":"https","uri":"https://deploy.example.net/windows11/install.wim","sha256":"` + h + `"}}`,
		"placeholder-com":  `{"schemaVersion":1,"image":{"source":"https","uri":"https://images.example.com/win11.wim","sha256":"` + h + `"}}`,
		"zero-hash":        `{"schemaVersion":1,"image":{"source":"https","uri":"https://images.lab.internal/w.wim","sha256":"` + strings.Repeat("0", 64) + `"}}`,
		"no-uri":           `{"schemaVersion":1,"image":{"source":"https","sha256":"` + h + `"}}`,
		"http-uri":         `{"schemaVersion":1,"image":{"source":"https","uri":"http://images.lab.internal/w.wim","sha256":"` + h + `"}}`,
		"short-hash":       `{"schemaVersion":1,"image":{"source":"https","uri":"https://images.lab.internal/w.wim","sha256":"abc"}}`,
		"malformed":        `{"schemaVersion":1,`,
		"bom":              "\ufeff" + testProfile,
	}
	for name, body := range bad {
		if err := os.WriteFile(filepath.Join(f.s.ManifestDir, name+".json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		code, out := f.do(t, "POST", "/api/armed", `{"mac":"52:54:00:12:34:56","manifest":"`+name+`"}`, "")
		if code != 400 || len(f.s.st.Armed) != 0 || !strings.Contains(out, name) {
			t.Errorf("profile %s: want 400 naming it, got %d %s (armed %d)", name, code, out, len(f.s.st.Armed))
		}
	}
}

// Gap 3, what ships: the profile in manifests/ (copied next to the Windows and Linux binaries) must not be armable as is, and no
// server default may point at the maintainer's host.
func TestShippedDefaultsDoNotPointAtTheMaintainerHost(t *testing.T) {
	f := newFixture(t, "")
	b, err := os.ReadFile(filepath.Join("..", "manifests", "win11-pro.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.s.ManifestDir, "shipped.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := f.do(t, "POST", "/api/armed", `{"mac":"52:54:00:12:34:56","manifest":"shipped"}`, ""); code != 400 {
		t.Errorf("the shipped profile can be armed as is: %d %s", code, out)
	}
	for _, p := range []string{"../manifests", "../tests/pxe", "."} { // the boot scripts are checked by Pester
		files, _ := filepath.Glob(filepath.Join(p, "*"))
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			if b, err := os.ReadFile(file); err == nil && bytes.Contains(b, []byte("spillerstech")) {
				t.Errorf("%s still names the maintainer's host", file)
			}
		}
	}
}

// --- the real binary: defaults and startup refusal (gap 2 and gap 3) ---

// TestMain lets a test run main() in a child process: M2W_TEST_MAIN holds the JSON argument list.
func TestMain(m *testing.M) {
	if args := os.Getenv("M2W_TEST_MAIN"); args != "" {
		var a []string
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			panic(err)
		}
		os.Args = append([]string{"minute2win-server"}, a...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runMain starts main() with args and env (M2W_TOKEN removed unless given). It returns once the process exits, or after wait if it
// is still running (then running is true and the process is killed).
func runMain(t *testing.T, wait time.Duration, token string, args ...string) (out string, running bool, err error) {
	t.Helper()
	a, _ := json.Marshal(args)
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	env := []string{"M2W_TEST_MAIN=" + string(a)}
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "M2W_") {
			env = append(env, e)
		}
	}
	if token != "" {
		env = append(env, "M2W_TOKEN="+token)
	}
	cmd.Env = env
	cmd.Dir = t.TempDir()
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err = cmd.Run()
	return buf.String(), ctx.Err() != nil, err
}

// Gap 2 and 3 defaults, read from the binary's own -h output.
func TestDefaultsAreLoopbackAndNoRemoteImageHost(t *testing.T) {
	out, _, _ := runMain(t, 20*time.Second, "", "-h")
	if !strings.Contains(out, `-listen string`) {
		t.Fatalf("no usage output: %s", out)
	}
	if !strings.Contains(out, `(default "127.0.0.1:8080")`) || strings.Contains(out, `(default ":8080")`) {
		t.Errorf("-listen must default to loopback:\n%s", out)
	}
	if strings.Contains(out, "spillerstech") || strings.Contains(out, `(default "https://`) {
		t.Errorf("-image-base must have no default host:\n%s", out)
	}
	if !strings.Contains(out, "-insecure-no-token") {
		t.Errorf("no explicit opt-out flag:\n%s", out)
	}
}

// Gap 2: a non-loopback listener without M2W_TOKEN refuses to start, and says exactly what to do. 192.0.2.1 (TEST-NET-1) is never
// a local address, so the old binary fails to bind rather than opening a port on this machine; the new one refuses before binding.
func TestRefusesNetworkListenerWithoutToken(t *testing.T) {
	// Wildcard binds are covered by TestListenExposure without opening a port (a test binary listening on all interfaces makes
	// Windows Firewall prompt).
	for _, listen := range []string{"192.0.2.1:0"} {
		out, running, _ := runMain(t, 20*time.Second, "", "-listen", listen, "-data", "", "-open=false")
		if running {
			t.Fatalf("-listen %s with no token kept running:\n%s", listen, out)
		}
		for _, want := range []string{"M2W_TOKEN", "-insecure-no-token", "127.0.0.1:8080"} {
			if !strings.Contains(out, want) {
				t.Errorf("-listen %s: refusal does not mention %s:\n%s", listen, want, out)
			}
		}
	}
}

// Loopback with no token still starts (the preview); so does a LAN address once the opt-out is explicit (checked in the pure test).
func TestLoopbackWithoutTokenStarts(t *testing.T) {
	out, running, _ := runMain(t, 3*time.Second, "", "-listen", "127.0.0.1:0", "-data", "", "-open=false")
	if !running {
		t.Fatalf("loopback without a token should start:\n%s", out)
	}
}

// Gap 2, the decision itself, for the binds the child-process test cannot open without a firewall prompt.
func TestListenExposure(t *testing.T) {
	cases := []struct {
		listen, token string
		insecure      bool
		refuse, warn  bool
	}{
		{"127.0.0.1:8080", "", false, false, true}, // the default: starts, says targets can't reach it
		{"localhost:8080", "", false, false, true},
		{"[::1]:8080", "", false, false, true},
		{"127.0.0.1:8080", "t", false, false, false},
		{":8080", "", false, true, false}, // the old default, no token: refused
		{"0.0.0.0:8080", "", false, true, false},
		{"[::]:8080", "", false, true, false},
		{"192.168.1.50:8080", "", false, true, false},
		{":8080", "t", false, false, false}, // the documented deployment: token set
		{"192.168.1.50:8080", "t", false, false, false},
		{":8080", "", true, false, true}, // explicit opt-out: starts, loudly
		{"bad", "", false, true, false},  // not host:port: never treated as loopback
	}
	for _, c := range cases {
		warn, err := checkListen(c.listen, c.token, c.insecure)
		if (err != nil) != c.refuse || (warn != "") != c.warn {
			t.Errorf("checkListen(%q, token %v, insecure %v) = %q, %v", c.listen, c.token != "", c.insecure, warn, err)
		}
		if err != nil && !strings.Contains(err.Error(), "-insecure-no-token") {
			t.Errorf("%s: refusal must name every way out: %v", c.listen, err)
		}
	}
}

func TestCheckImageBase(t *testing.T) {
	for base, ok := range map[string]bool{
		"https://boot.lab.internal/windows11":   true,
		"http://192.168.1.10:8000/windows11":    true,
		"https://deploy.example.net/windows11":  false, // the docs' placeholder, pasted as is
		"https://example.com/w":                 false,
		"ftp://boot.lab.internal/w":             false,
		"boot.lab.internal/windows11":           false,
		"https:///windows11":                    false,
		"https://boot.lab.internal/w\nkernel x": false, // a line break would inject an iPXE command
	} {
		if err := checkImageBase(base); (err == nil) != ok {
			t.Errorf("checkImageBase(%q) = %v", base, err)
		}
	}
}

func TestDirectHost(t *testing.T) {
	for h, ok := range map[string]bool{"localhost": true, "localhost:8080": true, "localhost.:1": true, "127.0.0.1:8080": true, "[::1]:8080": true,
		"10.0.0.5": true, "deploy.lab.internal:8080": false, "localhost.attacker.example": false, "": false} {
		if directHost(h) != ok {
			t.Errorf("directHost(%q) = %v", h, !ok)
		}
	}
}
