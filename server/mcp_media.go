package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func randomID() string          { var b [16]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func hashBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func safeMediaName(name string) bool {
	return linuxFileRe.MatchString(name) && !strings.Contains(name, "..") && !strings.HasSuffix(name, ".") && filepath.IsLocal(name) && !strings.HasPrefix(name, "m2w-part-")
}

func (s *Server) mcpProfileGet(name string) toolResult {
	if !nameRe.MatchString(name) {
		return deploymentToolError(errBadManifest)
	}
	if s.protectedProfile(name) {
		return toolError("protected_file", "Server state/accounts/settings are not deployment profiles.", "Keep the profile directory separate from operator data.", false)
	}
	root, err := os.OpenRoot(s.ManifestDir)
	if err != nil {
		return fileToolError(err)
	}
	defer root.Close()
	b, err := root.ReadFile(name + ".json")
	if err != nil {
		return fileToolError(err)
	}
	if len(b) > 256<<10 {
		return toolError("profile_too_large", "Profile exceeds 256 KiB.", "Reduce the profile to deployment settings.", false)
	}
	var profile map[string]any
	if err := json.Unmarshal(b, &profile); err != nil {
		return deploymentToolError(err)
	}
	return toolOK(map[string]any{"name": name, "profile": profile, "sha256": hashBytes(b)})
}

func (s *Server) mcpProfileWrite(action, name string, a map[string]json.RawMessage) toolResult {
	if !nameRe.MatchString(name) || !filepath.IsLocal(name+".json") {
		return deploymentToolError(errBadManifest)
	}
	if s.protectedProfile(name) {
		return toolError("protected_file", "Server state/accounts/settings cannot be edited as deployment profiles.", "Keep the profile directory separate from operator data.", false)
	}
	if err := s.idleError(name); err != nil {
		return toolError("profile_in_use", err.Error(), "Disarm pending targets and wait for active deployments before changing this profile.", false)
	}
	root, err := os.OpenRoot(s.ManifestDir)
	if err != nil {
		return fileToolError(err)
	}
	defer root.Close()
	old, readErr := root.ReadFile(name + ".json")
	expected := argString(a, "expected_sha256")
	if readErr == nil {
		if expected == "" || !strings.EqualFold(expected, hashBytes(old)) {
			return toolError("profile_conflict", "Profile already exists and its hash was not matched.", "Read profile_get, review changes, then retry with its sha256 as expected_sha256.", false)
		}
	} else if !os.IsNotExist(readErr) {
		return fileToolError(readErr)
	} else if expected != "" {
		return toolError("profile_conflict", "Profile no longer exists.", "Refresh profiles_list before editing.", false)
	}
	if action == "profile_delete" {
		if os.IsNotExist(readErr) {
			return deploymentToolError(errBadManifest)
		}
		if err := root.Remove(name + ".json"); err != nil {
			return fileToolError(err)
		}
		return toolOK(map[string]any{"deleted": true, "name": name})
	}
	var profile map[string]any
	_ = json.Unmarshal(a["profile"], &profile)
	b, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return deploymentToolError(err)
	}
	if len(b) > 256<<10 {
		return toolError("profile_too_large", "Profile exceeds 256 KiB.", "Reduce the profile to deployment settings.", false)
	}
	if _, err := parseProfile(b, name); err != nil {
		return deploymentToolError(err)
	}
	if err := atomicRootWrite(root, name+".json", b); err != nil {
		return fileToolError(err)
	}
	return toolOK(map[string]any{"saved": true, "name": name, "sha256": hashBytes(b), "next_tool": "profile_validate"})
}
func atomicRootWrite(root *os.Root, name string, b []byte) error {
	tmp := "m2w-part-" + randomID()
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Rename(tmp, name)
}

type mediaJob struct {
	mu          sync.Mutex
	ID          string         `json:"job_id"`
	Operation   string         `json:"operation"`
	Area        string         `json:"area"`
	File        string         `json:"file"`
	Status      string         `json:"status"`
	Bytes       int64          `json:"bytes"`
	SHA256      string         `json:"sha256,omitempty"`
	PinVerified bool           `json:"pin_verified"`
	Error       map[string]any `json:"error,omitempty"`
	Created     time.Time      `json:"created_at"`
	Finished    *time.Time     `json:"finished_at,omitempty"`
	cancel      context.CancelFunc
}

func (j *mediaJob) snapshot() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	b, _ := json.Marshal(j)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	out["terminal"] = j.Status == "done" || j.Status == "failed" || j.Status == "cancelled"
	out["success"] = j.Status == "done"
	out["publisher_authentication"] = "not_checked_by_server; verify the publisher metadata before supplying a pin"
	out["poll_after_seconds"] = 2
	return out
}
func (j *mediaJob) finish(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now().UTC()
	j.Finished = &now
	if err == nil {
		j.Status = "done"
		return
	}
	j.Status = "failed"
	code, next := "media_failed", "Check the configured media root, source access, disk space and expected hash. Retry with a new job."
	if err == context.Canceled || err == context.DeadlineExceeded {
		j.Status = "cancelled"
		code = "cancelled"
		next = "The unpublished partial was removed. Restart the job if still needed."
	}
	j.Error = map[string]any{"code": code, "message": err.Error(), "next_steps": []string{next}, "retryable": true}
}

func (s *Server) mediaRoot(area string) (string, error) {
	switch area {
	case "media":
		return s.MCP.MediaDir, nil
	case "pxe":
		return s.MCP.PXEDir, nil
	case "linux":
		if s.LinuxAssets != "" {
			return s.LinuxAssets, nil
		}
	}
	return "", fmt.Errorf("media area %q is not configured; use setup_status and server_configure", area)
}
func (s *Server) mcpMedia(_ context.Context, action string, a map[string]json.RawMessage) toolResult {
	if action == "jobs_list" {
		s.MCP.jobsMu.Lock()
		defer s.MCP.jobsMu.Unlock()
		jobs := []any{}
		for _, id := range sortedKeys(s.MCP.jobs) {
			jobs = append(jobs, s.MCP.jobs[id].snapshot())
		}
		return toolOK(map[string]any{"jobs": jobs})
	}
	if action == "job_get" || action == "job_cancel" {
		s.MCP.jobsMu.Lock()
		j := s.MCP.jobs[argString(a, "job_id")]
		s.MCP.jobsMu.Unlock()
		if j == nil {
			return toolError("job_not_found", "Unknown media job ID. Jobs do not survive server restarts.", "Use the ID returned by the media operation, or inspect media_list after a restart.", false)
		}
		if action == "job_cancel" {
			j.cancel()
		}
		return toolOK(j.snapshot())
	}
	area, file := argString(a, "area"), argString(a, "file")
	dir, err := s.mediaRoot(area)
	if err != nil {
		return fileToolError(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fileToolError(err)
	}
	if action == "media_list" {
		defer root.Close()
		f, err := root.Open(".")
		if err != nil {
			return fileToolError(err)
		}
		defer f.Close()
		entries, err := f.ReadDir(1001)
		if err != nil && err != io.EOF {
			return fileToolError(err)
		}
		if len(entries) > 1000 {
			return toolError("too_many_files", "Media inventory exceeds 1000 files.", "Use a dedicated deployment-media directory.", false)
		}
		files := []any{}
		for _, e := range entries {
			if !safeMediaName(e.Name()) || !e.Type().IsRegular() {
				continue
			}
			st, err := e.Info()
			if err == nil {
				files = append(files, map[string]any{"file": e.Name(), "size_bytes": st.Size(), "modified": st.ModTime()})
			}
		}
		return toolOK(map[string]any{"area": area, "files": files})
	}
	if !safeMediaName(file) {
		root.Close()
		return toolError("invalid_filename", "Media needs a plain, local filename with no directories or reserved names.", "Choose a name such as debian-linux or install.wim.", false)
	}
	expected := strings.ToLower(argString(a, "sha256"))
	if (action == "media_fetch" || expected != "") && (!sha256Re.MatchString(expected) || strings.Trim(expected, "0") == "") {
		root.Close()
		return toolError("invalid_hash", "A real SHA-256 pin is required, not a placeholder.", "Obtain the hash from verified publisher metadata before downloading.", false)
	}
	maxBytes := argInt(a, "max_bytes", 32<<30)
	if maxBytes < 1 || maxBytes > 32<<30 {
		root.Close()
		return toolError("invalid_size_limit", "max_bytes must be 1 to 34359738368.", "Set a bounded download limit appropriate to the media size.", false)
	}
	if action == "media_fetch" {
		u, err := url.Parse(argString(a, "url"))
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			root.Close()
			return toolError("invalid_url", "Media downloads require a direct HTTPS URL without embedded credentials or a fragment.", "Use the official publisher's media URL, including its temporary query only when needed.", false)
		}
	}
	var source *os.File
	if action == "media_import" {
		if s.MCP.ImportDir == "" {
			root.Close()
			return toolError("import_disabled", "No -mcp-import-dir was approved at startup.", "Restart with a dedicated import root, or place files directly in the configured media root and use media_inspect.", false)
		}
		srcRoot, err := os.OpenRoot(s.MCP.ImportDir)
		if err == nil {
			source, err = srcRoot.Open(argString(a, "source"))
			srcRoot.Close()
		}
		if err != nil {
			root.Close()
			return fileToolError(err)
		}
		st, err := source.Stat()
		if err != nil || !st.Mode().IsRegular() {
			source.Close()
			root.Close()
			return fileToolError(fmt.Errorf("import source must be a regular file"))
		}
	}
	if action != "media_inspect" {
		if _, err := root.Lstat(file); err == nil || !os.IsNotExist(err) {
			root.Close()
			if source != nil {
				source.Close()
			}
			return toolError("media_exists", "Destination exists or cannot be inspected; it was not replaced.", "Use a different filename and update profile pins after validation.", false)
		}
	}
	s.MCP.jobsMu.Lock()
	if s.MCP.jobs == nil {
		s.MCP.jobs = map[string]*mediaJob{}
	}
	active := 0
	for id, j := range s.MCP.jobs {
		j.mu.Lock()
		done := j.Finished != nil
		j.mu.Unlock()
		if !done {
			active++
		} else if len(s.MCP.jobs) >= 128 {
			delete(s.MCP.jobs, id)
		}
	}
	if active >= 4 {
		s.MCP.jobsMu.Unlock()
		root.Close()
		if source != nil {
			source.Close()
		}
		return toolError("jobs_busy", "Four media jobs are already running.", "Poll or cancel existing jobs before starting another.", true)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	j := &mediaJob{ID: randomID(), Operation: action, Area: area, File: file, Status: "running", Created: time.Now().UTC(), cancel: cancel}
	s.MCP.jobs[j.ID] = j
	s.MCP.jobsMu.Unlock()
	rawURL := argString(a, "url")
	go func() {
		defer root.Close()
		defer cancel()
		if source != nil {
			defer source.Close()
		}
		err := runMediaJob(ctx, j, root, source, rawURL, expected, maxBytes, s.MCP.mediaTransport)
		j.finish(err)
	}()
	return toolOK(j.snapshot())
}

type progressWriter struct{ j *mediaJob }

func (w progressWriter) Write(b []byte) (int, error) {
	w.j.mu.Lock()
	w.j.Bytes += int64(len(b))
	w.j.mu.Unlock()
	return len(b), nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

func runMediaJob(ctx context.Context, j *mediaJob, root *os.Root, source *os.File, rawURL, expected string, maxBytes int64, transport http.RoundTripper) error {
	var reader io.Reader = source
	if j.Operation == "media_fetch" {
		req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
		if err != nil {
			return fmt.Errorf("invalid media URL")
		}
		client := &http.Client{Timeout: 30 * time.Minute, CheckRedirect: safeMediaRedirect, Transport: transport}
		res, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("download connection or TLS failure; verify source reachability and certificate trust")
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return fmt.Errorf("download returned HTTP %d; check access, signed-link expiry and media path", res.StatusCode)
		}
		if res.ContentLength > maxBytes {
			return fmt.Errorf("media exceeds max_bytes (%d > %d)", res.ContentLength, maxBytes)
		}
		reader = res.Body
	} else if j.Operation == "media_inspect" {
		f, err := root.Open(j.File)
		if err != nil {
			return err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			return fmt.Errorf("media must be a regular file")
		}
		reader = f
	}
	if reader == nil {
		return fmt.Errorf("missing media source")
	}
	h := sha256.New()
	writers := []io.Writer{h, progressWriter{j}}
	var tmp string
	var output *os.File
	if j.Operation != "media_inspect" {
		tmp = "m2w-part-" + randomID()
		var err error
		output, err = root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer func() { output.Close(); root.Remove(tmp) }()
		writers = append(writers, output)
	}
	n, err := io.Copy(io.MultiWriter(writers...), io.LimitReader(contextReader{ctx, reader}, maxBytes+1))
	if err != nil {
		return err
	}
	if n > maxBytes {
		return fmt.Errorf("media exceeds max_bytes")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))
	j.mu.Lock()
	j.SHA256 = actual
	j.mu.Unlock()
	if expected != "" && !strings.EqualFold(expected, actual) {
		return fmt.Errorf("SHA-256 mismatch: expected %s, received %s; partial media removed", expected, actual)
	}
	if output != nil {
		if err := output.Sync(); err != nil {
			return err
		}
		if err := output.Close(); err != nil {
			return err
		}
		if err := root.Link(tmp, j.File); err != nil {
			return fmt.Errorf("could not publish media without overwriting: %w", err)
		}
		if err := root.Remove(tmp); err != nil {
			return fmt.Errorf("media published, but temporary link cleanup failed: %w", err)
		}
	}
	j.mu.Lock()
	j.PinVerified = expected != ""
	j.mu.Unlock()
	return nil
}
