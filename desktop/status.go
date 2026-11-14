package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type hostState struct {
	Armed       []json.RawMessage `json:"armed"`
	Deployments []struct {
		Status string `json:"status"`
	} `json:"deployments"`
	Degraded bool `json:"degraded"`
}

type trayStatus struct{ Label, Kind string }

func consoleURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.ContainsAny(raw, "\r\n\x00") {
		return "", errors.New("url must be an http(s) origin without credentials, path, query or fragment")
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}

func statusClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func fetchStatus(ctx context.Context, client *http.Client, origin string) (trayStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/api/state", nil)
	if err != nil {
		return trayStatus{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return trayStatus{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return trayStatus{}, fmt.Errorf("state returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return trayStatus{}, err
	}
	if len(body) > 1<<20 {
		return trayStatus{}, errors.New("state response exceeds 1 MiB")
	}
	// Required fields prevent an unrelated HTTP 200/JSON response looking like an idle host.
	var envelope map[string]json.RawMessage
	if err = json.Unmarshal(body, &envelope); err != nil {
		return trayStatus{}, err
	}
	for _, key := range []string{"armed", "deployments", "degraded"} {
		if _, ok := envelope[key]; !ok {
			return trayStatus{}, fmt.Errorf("missing state field %s", key)
		}
	}
	if string(envelope["degraded"]) != "true" && string(envelope["degraded"]) != "false" {
		return trayStatus{}, errors.New("state degraded field must be boolean")
	}
	var state hostState
	if err = json.Unmarshal(body, &state); err != nil {
		return trayStatus{}, err
	}
	return summarize(state), nil
}

func summarize(s hostState) trayStatus {
	active := 0
	for _, d := range s.Deployments {
		if d.Status == "booting" || d.Status == "running" {
			active++
		}
	}
	if s.Degraded {
		return trayStatus{fmt.Sprintf("Host degraded | %d armed, %d deploying", len(s.Armed), active), "degraded"}
	}
	if active > 0 {
		return trayStatus{fmt.Sprintf("%d deploying | %d armed", active, len(s.Armed)), "deploying"}
	}
	if len(s.Armed) > 0 {
		return trayStatus{fmt.Sprintf("%d armed | waiting for network boot", len(s.Armed)), "armed"}
	}
	return trayStatus{"Host online | no armed machines", "idle"}
}
