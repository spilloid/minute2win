package main

// Adversarial review of the arm-hardening branch (astra, 2026-10-19): each finding reproduced here before it was fixed.
//   1. -listen with a host name was checked against one DNS answer and bound against another, so a name that resolved to loopback
//      for the check and to a LAN address for the bind opened an unauthenticated network listener.
//   2. GET /boot/{mac} consumes an arm, and CrossOriginProtection lets GET through, so <img src=".../boot/MAC"> on any web page the
//      operator opened used up an arm before the machine booted.
//   Re-review: a header check only stops requests that carry browser headers, and browsers send none to a plain-http LAN address
//      on an image load. GET /boot/{mac} now changes nothing; the arm is consumed only by a one-time URL in its response.

import (
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type fakeListener struct {
	addr   net.Addr
	closed bool
}

func (l *fakeListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (l *fakeListener) Close() error              { l.closed = true; return nil }
func (l *fakeListener) Addr() net.Addr            { return l.addr }

// Finding 1: the name resolves to loopback when checked, then the bind lands on a LAN address (a DNS change or rebinding between
// the two lookups). No token and no -insecure-no-token: the server must not end up with that listener.
func TestListenerCheckedAsBoundNotAsResolved(t *testing.T) {
	old := lookupIP
	t.Cleanup(func() { lookupIP = old })
	lookupIP = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("127.0.0.1")}, nil }
	var bound *fakeListener
	lanBind := func(_, _ string) (net.Listener, error) {
		bound = &fakeListener{addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 8080}}
		return bound, nil
	}
	ln, _, err := openListener("deploy.lab.internal:8080", "", false, lanBind)
	if err == nil {
		t.Fatalf("listener on %v accepted without a token", ln.Addr())
	}
	if bound == nil || !bound.closed {
		t.Errorf("the refused listener was not closed")
	}
	if !strings.Contains(err.Error(), "192.0.2.10") || !strings.Contains(err.Error(), "-insecure-no-token") {
		t.Errorf("refusal should name the bound address and the ways out: %v", err)
	}
	// With a token, or the explicit opt-out, the same bind is accepted.
	for _, c := range []struct {
		token    string
		insecure bool
	}{{"t", false}, {"", true}} {
		if _, _, err := openListener("deploy.lab.internal:8080", c.token, c.insecure, lanBind); err != nil {
			t.Errorf("token %v insecure %v: %v", c.token != "", c.insecure, err)
		}
	}
}

// A real loopback bind by name still starts without a token, and the refusal before binding still happens for literal addresses.
func TestListenerLoopbackByNameStillWorks(t *testing.T) {
	ln, warn, err := openListener("localhost:0", "", false, net.Listen)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if ip := ln.Addr().(*net.TCPAddr).IP; !ip.IsLoopback() {
		t.Fatalf("bound to %v", ip)
	}
	if warn == "" {
		t.Error("no loopback-only warning")
	}
	called := false
	_, _, err = openListener("192.0.2.1:0", "", false, func(_, _ string) (net.Listener, error) { called = true; return nil, nil })
	if err == nil || called {
		t.Errorf("a literal LAN address without a token must be refused before binding (err %v, bound %v)", err, called)
	}
}

// Finding 2: a page on another site loads /boot/<armed MAC> as an image, a script, a fetch or a navigation. Starting from an
// armed MAC, none of these may consume the arm; then iPXE (no browser headers) still gets the deployment script.
func TestCrossSiteBootDoesNotConsumeArm(t *testing.T) {
	f := newFixture(t, "")
	const mac = "52:54:00:12:34:56"
	f.arm(t, mac)
	for name, hdr := range map[string]map[string]string{
		"img tag":                 {"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "image"},
		"navigation from a page":  {"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"},
		"same-site, another port": {"Sec-Fetch-Site": "same-site", "Sec-Fetch-Dest": "image"},
		"fetch, Origin only":      {"Origin": evil},
		"sandboxed, Origin null":  {"Origin": "null"},
	} {
		code, out := f.req(t, "GET", "/boot/"+mac, "", "", hdr)
		if f.s.st.Armed[mac] == nil {
			t.Fatalf("%s: consumed the arm (%d %s)", name, code, out)
		}
		if code != http.StatusForbidden || strings.Contains(out, "wimboot") {
			t.Errorf("%s: want 403 without a boot script, got %d %s", name, code, out)
		}
	}
	code, out := f.boot(t, mac) // iPXE: no browser headers, follows the chain
	if code != 200 || !strings.Contains(out, "wimboot") || f.s.st.Armed[mac] != nil {
		t.Fatalf("iPXE did not get the deployment: %d %s", code, out)
	}
}

// ticketURL returns the one-time URL GET /boot/{mac} chains to, as a path.
func (f *fixture) ticketURL(t *testing.T, mac string) string {
	t.Helper()
	_, body := f.req(t, "GET", "/boot/"+mac, "", "", nil)
	for _, line := range strings.Split(body, "\n") {
		if u, ok := strings.CutPrefix(line, "chain "+f.ts.URL); ok {
			return u
		}
	}
	t.Fatalf("no chain line for %s:\n%s", mac, body)
	return ""
}

// Re-review, finding 2: browsers send no Fetch Metadata to a plain-http LAN address (not a potentially trustworthy origin), and
// an <img> load sends no Origin, so a foreign page's request for /boot/<armed MAC> arrives exactly like iPXE's: no browser
// headers at all. Such a blind GET must not consume the arm; only the one-time URL in its response may, and a page on another
// site cannot read that response.
func TestHeaderlessBlindGETDoesNotConsumeArm(t *testing.T) {
	f := newFixture(t, "")
	const mac = "52:54:00:12:34:56"
	f.arm(t, mac)
	for i := 0; i < 3; i++ {
		code, out := f.req(t, "GET", "/boot/"+mac, "", "", nil)
		if code != 200 || strings.Contains(out, "wimboot") {
			t.Fatalf("blind GET: want the chain script only, got %d %s", code, out)
		}
	}
	if f.s.st.Armed[mac] == nil {
		t.Fatal("a headerless GET /boot/<mac> (an <img> on a page served over plain http) consumed the arm")
	}
	if len(f.s.st.Deployments) != 0 {
		t.Fatalf("a blind GET started a deployment: %v", f.s.st.Deployments)
	}
	// Guessing is not enough either: a wrong ticket, a ticket-shaped path for another MAC, or an empty one consumes nothing.
	for _, p := range []string{"/boot/" + mac + "/00000000000000000000000000000000", "/boot/" + mac + "/x", "/boot/" + mac + "/"} {
		if _, out := f.req(t, "GET", p, "", "", nil); strings.Contains(out, "wimboot") || f.s.st.Armed[mac] == nil {
			t.Fatalf("%s consumed the arm: %s", p, out)
		}
	}
}

// The ticket is one-time, secret and tied to its arm: it is not in /api/state, the console or the state file; it works once;
// it dies with the arm (consumed, disarmed, expired, re-armed); and it only fits its own MAC.
func TestBootTicketIsOneTimeAndSecret(t *testing.T) {
	f := newFixture(t, "")
	const mac, other = "52:54:00:12:34:56", "52:54:00:00:00:99"
	f.arm(t, mac)
	f.arm(t, other)
	u := f.ticketURL(t, mac)
	ticket := u[strings.LastIndex(u, "/")+1:]
	if len(ticket) != 32 {
		t.Fatalf("ticket %q: want 128 random bits as hex", ticket)
	}
	if again := f.ticketURL(t, mac); again != u {
		t.Fatalf("a second GET /boot/<mac> changed the ticket (%s, %s): a page's blind GET must not break iPXE's chain", u, again)
	}
	_, state := f.do(t, "GET", "/api/state", "", "")
	_, page := f.do(t, "GET", "/ui/armed", "", "")
	file, _ := os.ReadFile(f.s.StatePath)
	for where, s := range map[string]string{"/api/state": state, "/ui/armed": page, "state file": string(file)} {
		if strings.Contains(s, ticket) {
			t.Errorf("the ticket leaks through %s", where)
		}
	}
	if _, out := f.req(t, "GET", "/boot/"+other+"/"+ticket, "", "", nil); strings.Contains(out, "wimboot") || f.s.st.Armed[other] == nil {
		t.Fatalf("one MAC's ticket consumed another MAC's arm: %s", out)
	}
	if _, out := f.req(t, "GET", u, "", "", nil); !strings.Contains(out, "wimboot") || f.s.st.Armed[mac] != nil {
		t.Fatalf("the ticket did not deploy: %s", out)
	}
	f.arm(t, mac) // re-armed: the old URL must not consume the new arm
	if _, out := f.req(t, "GET", u, "", "", nil); strings.Contains(out, "wimboot") || f.s.st.Armed[mac] == nil {
		t.Fatalf("a used ticket worked again: %s", out)
	}
	u2 := f.ticketURL(t, mac)
	if u2 == u {
		t.Fatal("re-arming kept the old ticket")
	}
	f.do(t, "DELETE", "/api/armed/"+mac, "", "")
	if _, out := f.req(t, "GET", u2, "", "", nil); strings.Contains(out, "wimboot") {
		t.Fatalf("a disarmed MAC's ticket deployed: %s", out)
	}
	f.arm(t, mac)
	u3 := f.ticketURL(t, mac)
	f.now = f.now.Add(61 * time.Minute) // the fixture's ArmTTL is an hour
	if _, out := f.req(t, "GET", u3, "", "", nil); strings.Contains(out, "wimboot") {
		t.Fatalf("an expired arm's ticket deployed: %s", out)
	}
}

// The operator opening the URL themselves (address bar: Sec-Fetch-Site none) or the server's own page is not refused; like
// every other first request it only gets the chain script.
func TestBootFromOwnOriginOrAddressBarStillWorks(t *testing.T) {
	f := newFixture(t, "")
	for i, hdr := range []map[string]string{
		{"Sec-Fetch-Site": "none", "Sec-Fetch-Mode": "navigate"},
		{"Sec-Fetch-Site": "same-origin", "Origin": f.ts.URL},
	} {
		mac := []string{"52:54:00:00:00:01", "52:54:00:00:00:02"}[i]
		f.arm(t, mac)
		if code, out := f.req(t, "GET", "/boot/"+mac, "", "", hdr); code != 200 || !strings.Contains(out, "chain "+f.ts.URL+"/boot/"+mac+"/") {
			t.Errorf("%v: %d %s", hdr, code, out)
		}
	}
}

// What iPXE receives on the first request: an iPXE script that only chains the one-time URL on this server, with the same
// scheme and host iPXE used, and nothing taken from the request path.
func TestBootChainScript(t *testing.T) {
	f := newFixture(t, "")
	const mac = "52:54:00:12:34:56"
	f.arm(t, "52-54-00-12-34-56")
	_, out := f.req(t, "GET", "/boot/52-54-00-12-34-56", "", "", nil)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || lines[0] != "#!ipxe" || !strings.HasPrefix(lines[1], "echo ") ||
		!strings.HasPrefix(lines[2], "chain "+f.ts.URL+"/boot/"+mac+"/") {
		t.Fatalf("unexpected chain script:\n%s", out)
	}
}
