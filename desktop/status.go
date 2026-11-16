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
	ArmedCount     *int              `json:"armedCount"`
	DeployingCount *int              `json:"deployingCount"`
	Armed          []json.RawMessage `json:"armed"`
	Deployments    []struct {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/api/status", nil)
	if err != nil {
		return trayStatus{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return trayStatus{}, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, origin+"/api/state", nil)
		if err != nil {
			return trayStatus{}, err
		}
		resp, err = client.Do(req)
		if err != nil {
			return trayStatus{}, err
		}
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
	required := []string{"armed", "deployments", "degraded"}
	if _, ok := envelope["armedCount"]; ok {
		required = []string{"armedCount", "deployingCount", "degraded"}
	}
	for _, key := range required {
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
	if len(required) > 0 && required[0] == "armedCount" && (state.ArmedCount == nil || state.DeployingCount == nil || *state.ArmedCount < 0 || *state.DeployingCount < 0) {
		return trayStatus{}, errors.New("invalid status counts")
	}
	return summarize(state), nil
}

func summarize(s hostState) trayStatus {
	armed := len(s.Armed)
	active := 0
	for _, d := range s.Deployments {
		if d.Status == "booting" || d.Status == "running" {
			active++
		}
	}
	if s.ArmedCount != nil && s.DeployingCount != nil {
		armed, active = *s.ArmedCount, *s.DeployingCount
	}
	if s.Degraded {
		return trayStatus{fmt.Sprintf("Host degraded | %d armed, %d deploying", armed, active), "degraded"}
	}
	if active > 0 {
		return trayStatus{fmt.Sprintf("%d deploying | %d armed", active, armed), "deploying"}
	}
	if armed > 0 {
		return trayStatus{fmt.Sprintf("%d armed | waiting for network boot", armed), "armed"}
	}
	return trayStatus{"Host online | no armed machines", "idle"}
}
