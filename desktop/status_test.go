package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConsoleURL(t *testing.T) {
	for _, raw := range []string{"file:///tmp/test", "https://user:secret@example.com", "http://example.com/path", "http://example.com/?token=x", "http://example.com/#x", "http://", "http://example.com\n"} {
		if _, err := consoleURL(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, raw := range []string{"http://127.0.0.1:8080", "https://console.example.com/", "http://[::1]:8080"} {
		if _, err := consoleURL(raw); err != nil {
			t.Errorf("refused %q: %v", raw, err)
		}
	}
}

func TestFetchStatus(t *testing.T) {
	for _, tc := range []struct {
		name, body, kind string
		code             int
	}{
		{"idle", `{"armed":[],"deployments":[],"degraded":false}`, "idle", 200},
		{"armed", `{"armed":[{}],"deployments":[],"degraded":false}`, "armed", 200},
		{"active", `{"armed":[{}],"deployments":[{"status":"running"},{"status":"booting"},{"status":"done"},{"status":"failed"}],"degraded":false}`, "deploying", 200},
		{"degraded priority", `{"armed":[{}],"deployments":[{"status":"running"}],"degraded":true}`, "degraded", 200},
		{"history", `{"armed":null,"deployments":[{"status":"done"},{"status":"failed"}],"degraded":false}`, "idle", 200},
		{"unrelated JSON", `{}`, "", 200},
		{"wrong types", `{"armed":{},"deployments":[],"degraded":false}`, "", 200},
		{"null degraded", `{"armed":[],"deployments":[],"degraded":null}`, "", 200},
		{"bad JSON", `<html>login</html>`, "", 200},
		{"error", `unavailable`, "", 503},
		{"oversize", strings.Repeat(" ", (1<<20)+1), "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/state" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				w.WriteHeader(tc.code)
				io.WriteString(w, tc.body)
			}))
			defer s.Close()
			status, err := fetchStatus(context.Background(), statusClient(), s.URL)
			if tc.kind == "" {
				if err == nil {
					t.Fatal("invalid response accepted")
				}
				return
			}
			if err != nil || status.Kind != tc.kind {
				t.Fatalf("%+v, %v", status, err)
			}
			if tc.name == "active" && !strings.Contains(status.Label, "2 deploying | 1 armed") {
				t.Fatal(status.Label)
			}
		})
	}
}

func TestStatusDoesNotFollowRedirectOrIgnoreCancellation(t *testing.T) {
	visited := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { visited = true }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer s.Close()
	if _, err := fetchStatus(context.Background(), statusClient(), s.URL); err == nil {
		t.Fatal("redirect accepted")
	}
	if visited {
		t.Fatal("followed redirect")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fetchStatus(ctx, statusClient(), s.URL); err == nil {
		t.Fatal("cancellation ignored")
	}
}
