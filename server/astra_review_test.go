package main

// Adversarial review of the arm-hardening branch (astra, 2026-10-19): each finding reproduced here before it was fixed.
//   1. -listen with a host name was checked against one DNS answer and bound against another, so a name that resolved to loopback
//      for the check and to a LAN address for the bind opened an unauthenticated network listener.
//   2. GET /boot/{mac} consumes an arm, and CrossOriginProtection lets GET through, so <img src=".../boot/MAC"> on any web page the
//      operator opened used up an arm before the machine booted.

import (
	"net"
	"net/http"
	"strings"
	"testing"
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
	code, out := f.req(t, "GET", "/boot/"+mac, "", "", map[string]string{"User-Agent": "iPXE/1.21.1+"})
	if code != 200 || !strings.Contains(out, "wimboot") || f.s.st.Armed[mac] != nil {
		t.Fatalf("iPXE did not get the deployment: %d %s", code, out)
	}
}

// The operator opening the URL themselves (address bar: Sec-Fetch-Site none) or the server's own page is not cross-site.
func TestBootFromOwnOriginOrAddressBarStillWorks(t *testing.T) {
	f := newFixture(t, "")
	for i, hdr := range []map[string]string{
		{"Sec-Fetch-Site": "none", "Sec-Fetch-Mode": "navigate"},
		{"Sec-Fetch-Site": "same-origin", "Origin": f.ts.URL},
	} {
		mac := []string{"52:54:00:00:00:01", "52:54:00:00:00:02"}[i]
		f.arm(t, mac)
		if code, out := f.req(t, "GET", "/boot/"+mac, "", "", hdr); code != 200 || !strings.Contains(out, "wimboot") {
			t.Errorf("%v: %d %s", hdr, code, out)
		}
	}
}
