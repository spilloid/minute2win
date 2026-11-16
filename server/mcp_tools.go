package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type toolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`
	Admin       bool           `json:"-"`
}

func field(kind, description string) map[string]any {
	return map[string]any{"type": kind, "description": description}
}
func enumField(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}
func tool(name, desc string, admin, read, destructive bool, required []string, props map[string]any) toolSpec {
	if props == nil {
		props = map[string]any{}
	}
	if required == nil {
		required = []string{}
	}
	return toolSpec{name, desc, map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}, map[string]any{"readOnlyHint": read, "destructiveHint": destructive, "idempotentHint": read, "openWorldHint": true}, admin}
}

func mcpTools() []toolSpec {
	name := field("string", "Profile filename without .json (1 to 64 letters, digits, dots, dashes or underscores).")
	mac := field("string", "Exact target NIC MAC, for example 52:54:00:12:34:56.")
	area := enumField("media", "linux", "pxe")
	file := field("string", "Plain filename, without directories. Files are confined to the selected operator-approved root.")
	return []toolSpec{
		tool("whoami", "Show authenticated account, role and allowed profiles. A server token is an administrator credential.", false, true, false, nil, nil),
		tool("setup_status", "Inspect host, network addresses, configuration, PXE, active deployments and setup blockers. Does not arm or boot.", false, true, false, nil, nil),
		tool("setup_guide", "Get step-by-step media acquisition and setup instructions with official sources for one installer.", false, true, false, []string{"installer"}, map[string]any{"installer": enumField("windows", "debian", "ubuntu", "proxmox", "bazzite")}),
		tool("server_configure", "Change Windows image base, Linux media location or arm TTL while idle. Saves to the data directory when configured; explicit startup flags override saved values on restart. Returns restart flags. Never restarts the host.", true, false, true, nil, map[string]any{"image_base": field("string", "Boot files URL; empty disables Windows."), "use_media_for_linux": field("boolean", "Use the configured MCP media directory for Linux assets."), "arm_ttl_seconds": field("integer", "60 to 86400 seconds.")}),
		tool("profiles_list", "List profiles and their validation errors. Listing does not download media or consume an arm.", false, true, false, nil, nil),
		tool("profile_get", "Read an allowed deployment profile.", false, true, false, []string{"name"}, map[string]any{"name": name}),
		tool("profile_template", "Get a Windows or experimental Linux profile template with placeholders and required media. Fill every placeholder before saving.", false, true, false, []string{"installer"}, map[string]any{"installer": enumField("windows", "debian", "ubuntu", "proxmox", "bazzite")}),
		tool("profile_put", "Validate and atomically create or replace a profile. Refuses edits to profiles used by arms or active deployments. Use expected_sha256 for optimistic concurrency on replacement.", true, false, true, []string{"name", "profile"}, map[string]any{"name": name, "profile": field("object", "Complete profile object, with real image hashes and no secrets."), "expected_sha256": field("string", "Required when replacing; SHA-256 returned by profile_get.")}),
		tool("profile_delete", "Delete an unused profile with an exact expected SHA-256.", true, false, true, []string{"name", "expected_sha256"}, map[string]any{"name": name, "expected_sha256": field("string", "SHA-256 returned by profile_get.")}),
		tool("profile_validate", "Check profile and configured media without arming. Linux hashes are checked. With probe=true, bounded HTTP checks test Windows image and boot files from the host; this does not prove target reachability or WinPE certificate trust.", false, true, false, []string{"name"}, map[string]any{"name": name, "probe": field("boolean", "Probe hosted Windows files with HEAD; never downloads the full image.")}),
		tool("media_list", "List regular files and sizes in an approved media root. SHA-256 is computed only by media_inspect.", true, true, false, []string{"area"}, map[string]any{"area": area}),
		tool("media_inspect", "Start a cancellable job to hash a local media file. Poll job_get. A computed hash alone is not publisher verification.", true, true, false, []string{"area", "file"}, map[string]any{"area": area, "file": file}),
		tool("media_fetch", "Start a bounded HTTPS download with a required independently verified SHA-256 and byte limit. Publishes only on a matching hash, never overwrites. Poll job_get; job_cancel aborts. Windows ISO license/download choices remain with the user.", true, false, false, []string{"area", "file", "url", "sha256", "max_bytes"}, map[string]any{"area": area, "file": file, "url": field("string", "Direct HTTPS URL with no embedded credentials."), "sha256": field("string", "Verified publisher SHA-256, 64 hex digits."), "max_bytes": field("integer", "Maximum download size, up to 32 GiB.")}),
		tool("media_import", "Copy and hash a file from the configured -mcp-import-dir into an approved media root. Source is relative and confined; no shell commands. Never overwrites. Poll job_get.", true, false, false, []string{"source", "area", "file"}, map[string]any{"source": field("string", "Relative file path inside -mcp-import-dir."), "area": area, "file": file, "sha256": field("string", "Optional verified publisher hash; omitted means computed only.")}),
		tool("job_get", "Get media job progress, bytes, hash, terminal outcome and actionable errors by stable job ID.", true, true, false, []string{"job_id"}, map[string]any{"job_id": field("string", "Job ID returned by a media tool.")}),
		tool("jobs_list", "Recover media job IDs after a client disconnect. Jobs are retained only in this server process.", true, true, false, nil, nil),
		tool("job_cancel", "Cancel a media job and clean unpublished partial files. Already published media is retained.", true, false, false, []string{"job_id"}, map[string]any{"job_id": field("string", "Media job ID.")}),
		tool("pxe_start", "Start built-in proxyDHCP and read-only TFTP using the approved PXE directory. Existing DHCP still assigns addresses. Requires a LAN HTTP listener and suitable UDP privileges.", true, false, false, nil, map[string]any{"advertise_ip": field("string", "Optional IPv4 LAN address (required on some multi-homed hosts)."), "file_x64": file, "file_bios": file, "file_arm64": file, "file_ia32": file, "dhcp_port": field("integer", "Default 67."), "proxy_port": field("integer", "Default 4011."), "tftp_port": field("integer", "Default 69.")}),
		tool("pxe_stop", "Stop PXE listeners while idle. This does not cancel a deployment already booted.", true, false, true, nil, nil),
		tool("machine_arm", "Arm an exact MAC for an allowed profile. DESTRUCTIVE: the next network boot erases its eligible internal disk. Reuse request_id on retry; a retained receipt cannot arm twice, even after boot/disarm/restart. This schedules deployment, not hardware power. Poll deployment_get with the returned run_id. Only use for the target/image the user authorized.", false, false, true, []string{"mac", "manifest", "request_id"}, map[string]any{"mac": mac, "manifest": name, "request_id": field("string", "Unique operation ID (1-64 letters/digits/dot/dash/underscore); reuse on timeout. Retained at least seven days.")}),
		tool("machine_disarm", "Revoke a pending arm within your image scope. If consumed, reports not_armed and cannot cancel the running installer.", false, false, false, []string{"mac"}, map[string]any{"mac": mac}),
		tool("deployments_list", "List arms and deployment progress within your image scope, without report keys or boot tickets.", false, true, false, nil, nil),
		tool("deployment_abandon", "Close tracking for a failed/stuck installer ONLY after the target has been stopped externally. Revokes target delivery/report capabilities and records outcome=unknown; does not stop, undo or verify an installation. Allows stale tracking to be recovered without hand-editing state.", true, false, true, []string{"mac", "target_stopped", "reason"}, map[string]any{"mac": mac, "target_stopped": field("boolean", "Must be true only after independently stopping the target installer."), "reason": field("string", "Reason for closing tracking, 1-512 characters; no secrets.")}),
		tool("deployment_get", "Monitor one target: pending arm, phase, status, timing, report age, stale/terminal flags, and recovery guidance. Supply run_id to avoid confusing an old run with a later deployment. Unknown or stale status is never success.", false, true, false, []string{"mac"}, map[string]any{"mac": mac, "run_id": field("string", "Optional stable run ID returned by machine_arm.")}),
		tool("accounts_list", "List local/SSO identities and roles without password or token hashes.", true, true, false, nil, nil),
		tool("account_put", "Create/update a local or SSO identity. Roles: viewer, operator, admin. Profiles scopes arming/disarming and monitoring; [\"*\"] grants all. OIDC maps by immutable subject, never email/username. Password is optional for SSO-only identities; omission preserves existing password. Policy updates revoke account sessions and tokens.", true, false, true, []string{"username", "role", "profiles"}, map[string]any{"username": field("string", "Local account label or provisioned proxy identity."), "oidc_subject": field("string", "Optional immutable OIDC sub to bind; overrides group-derived role/scope. Empty removes binding."), "role": enumField("viewer", "operator", "admin"), "profiles": map[string]any{"type": "array", "items": field("string", "Profile name or *."), "description": "Explicit allowed image profiles; empty grants none."}, "password": field("string", "New local password, at least 12 characters; omit for SSO-only or to preserve existing password.")}),
		tool("account_delete", "Remove an account and revoke its sessions and API tokens. Cannot delete yourself.", true, false, true, []string{"username"}, map[string]any{"username": field("string", "Account to remove.")}),
		tool("account_token_create", "Issue a personal MCP/API bearer token for an account. Returns the secret once; inherits that account's RBAC. Updating/deleting the account revokes it.", true, false, false, []string{"username"}, map[string]any{"username": field("string", "Existing account.")}),
	}
}

func mcpToolNamed(name string) (toolSpec, bool) {
	for _, t := range mcpTools() {
		if t.Name == name {
			return t, true
		}
	}
	return toolSpec{}, false
}
func validateToolArgs(t toolSpec, args map[string]json.RawMessage) error {
	props := t.InputSchema["properties"].(map[string]any)
	for _, k := range t.InputSchema["required"].([]string) {
		if _, ok := args[k]; !ok {
			return fmt.Errorf("missing required argument %s", k)
		}
	}
	for k, b := range args {
		p, ok := props[k]
		if !ok {
			return fmt.Errorf("unknown argument %s", k)
		}
		var v any
		if json.Unmarshal(b, &v) != nil {
			return fmt.Errorf("invalid argument %s", k)
		}
		m := p.(map[string]any)
		valid := false
		switch m["type"] {
		case "string":
			_, valid = v.(string)
		case "boolean":
			_, valid = v.(bool)
		case "object":
			_, valid = v.(map[string]any)
		case "array":
			a, ok := v.([]any)
			valid = ok
			for _, e := range a {
				if _, ok := e.(string); !ok {
					valid = false
				}
			}
		case "integer":
			f, ok := v.(float64)
			valid = ok && f == float64(int64(f))
		}
		if !valid {
			return fmt.Errorf("%s must be %s", k, m["type"])
		}
		if e, ok := m["enum"].([]string); ok {
			match := false
			for _, x := range e {
				if x == v {
					match = true
				}
			}
			if !match {
				return fmt.Errorf("%s must be one of %s", k, strings.Join(e, ", "))
			}
		}
	}
	return nil
}

type toolResult struct {
	Content           []map[string]any `json:"content"`
	StructuredContent map[string]any   `json:"structuredContent"`
	IsError           bool             `json:"isError,omitempty"`
}

func toolOK(data map[string]any) toolResult {
	b, _ := json.Marshal(data)
	return toolResult{Content: []map[string]any{{"type": "text", "text": string(b)}}, StructuredContent: data}
}
func toolError(code, message, next string, retry bool) toolResult {
	r := toolOK(map[string]any{"ok": false, "error": map[string]any{"code": code, "message": message, "next_steps": []string{next}, "retryable": retry}})
	r.IsError = true
	return r
}
func argString(a map[string]json.RawMessage, k string) string {
	var v string
	_ = json.Unmarshal(a[k], &v)
	return v
}
func argBool(a map[string]json.RawMessage, k string) bool {
	var v bool
	_ = json.Unmarshal(a[k], &v)
	return v
}
func argInt(a map[string]json.RawMessage, k string, def int64) int64 {
	if a[k] == nil {
		return def
	}
	// JSON Schema integers include values encoded as 600.0. The argument
	// validator already rejects fractions and out-of-range conversions.
	var v float64
	_ = json.Unmarshal(a[k], &v)
	return int64(v)
}

func (s *Server) callMCPTool(ctx context.Context, name string, a map[string]json.RawMessage) toolResult {
	p := principalFromContext(ctx)
	t, _ := mcpToolNamed(name)
	if (t.Admin && p.Role != "admin") || ((name == "machine_arm" || name == "machine_disarm") && !p.canOperate()) {
		return toolError("forbidden", "Your role cannot execute this tool.", "Ask an administrator to grant the required role.", false)
	}
	// Management mutations must exclude the console/boot path. Read operations
	// also share this lock so configuration and profile edits cannot race a boot.
	write := name == "server_configure" || name == "profile_put" || name == "profile_delete" || name == "pxe_start" || name == "pxe_stop" || name == "deployment_abandon"
	if write {
		s.controlMu.Lock()
		defer s.controlMu.Unlock()
	} else {
		s.controlMu.RLock()
		defer s.controlMu.RUnlock()
	}
	profile := argString(a, "name")
	if strings.HasPrefix(name, "profile_") && name != "profile_template" && !p.allows(profile) {
		return toolError("forbidden", "Profile is outside your image scope.", "Use profiles_list to see permitted images.", false)
	}
	switch name {
	case "whoami":
		return toolOK(map[string]any{"identity": p})
	case "setup_guide", "profile_template":
		i := argString(a, "installer")
		if name == "profile_template" {
			return toolOK(map[string]any{"installer": i, "profile": profileTemplate(i), "next_steps": []string{"Replace placeholders using verified media. Save with profile_put, then call profile_validate."}})
		}
		return toolOK(map[string]any{"installer": i, "guide": setupGuide(i), "next_tool": "setup_status"})
	case "setup_status":
		return s.mcpSetupStatus(p)
	case "server_configure":
		return s.mcpConfigure(a)
	case "profiles_list":
		names, err := s.manifests()
		if err != nil {
			return fileToolError(err)
		}
		items := []any{}
		for _, n := range names {
			if !p.allows(n) {
				continue
			}
			item := map[string]any{"name": n, "valid": true}
			if err := checkProfile(filepath.Join(s.ManifestDir, n+".json"), n); err != nil {
				item["valid"] = false
				item["error"] = err.Error()
			}
			items = append(items, item)
		}
		return toolOK(map[string]any{"profiles": items})
	case "profile_get":
		return s.mcpProfileGet(profile)
	case "profile_put", "profile_delete":
		return s.mcpProfileWrite(name, profile, a)
	case "profile_validate":
		return s.mcpValidate(ctx, profile, argBool(a, "probe"))
	case "media_list", "media_inspect", "media_import", "media_fetch", "job_get", "job_cancel", "jobs_list":
		return s.mcpMedia(ctx, name, a)
	case "pxe_start", "pxe_stop":
		return s.mcpPXE(name, a)
	case "machine_arm":
		image := argString(a, "manifest")
		if !p.allows(image) {
			return toolError("forbidden", "Image is outside your account scope.", "Use profiles_list and select an allowed image.", false)
		}
		requestID := argString(a, "request_id")
		if !nameRe.MatchString(requestID) {
			return toolError("invalid_request_id", "request_id must be 1 to 64 letters, digits, dots, dashes or underscores.", "Create a unique operation ID and keep it for every retry.", false)
		}
		armed, replayed, err := s.doArmRequest(argString(a, "mac"), image, p.Username, requestID)
		if err != nil {
			return deploymentToolError(err)
		}
		s.audit(p, "arm", armed.MAC, image, "armed")
		return toolOK(map[string]any{"arm_receipt": armed, "replayed": replayed, "run_id": armed.RunID, "warning": "The next network boot of a pending arm erases the target's eligible internal disk. A replayed receipt does not establish that it is still pending.", "next_tool": "deployment_get", "power_action_required": !replayed})
	case "machine_disarm":
		mac, ok := normMAC(argString(a, "mac"))
		if !ok {
			return deploymentToolError(errBadMAC)
		}
		if !s.mayDisarm(p, mac) {
			return toolError("forbidden", "Arm is outside your image scope.", "Ask an operator allowed to manage that image.", false)
		}
		removed, err := s.doDisarmAs(p, mac)
		if err != nil {
			return deploymentToolError(err)
		}
		if !removed {
			return toolError("not_armed", "No pending arm; a deployment that already booted was not cancelled.", "Call deployment_get; stop a running installer at its own console if required.", false)
		}
		s.audit(p, "disarm", mac, "", "disarmed")
		return toolOK(map[string]any{"disarmed": true, "mac": mac})
	case "deployments_list":
		return toolOK(s.mcpState(p))
	case "deployment_abandon":
		return s.mcpAbandon(p, a)
	case "deployment_get":
		return s.mcpDeployment(p, argString(a, "mac"), argString(a, "run_id"))
	case "accounts_list", "account_put", "account_delete", "account_token_create":
		return s.mcpAccounts(p, name, a)
	}
	return toolError("unsupported", "Tool is unavailable.", "Refresh tools/list.", false)
}

func fileToolError(err error) toolResult {
	return toolError("file_error", err.Error(), "Check the configured directory, permissions and filename; use setup_status for paths.", false)
}
func deploymentToolError(err error) toolResult {
	code, next, retry := "validation_failed", "Use profile_validate and setup_guide to correct the reported issue, then retry.", false
	switch err {
	case errImageScope:
		code = "forbidden"
		next = "Ask an operator permitted to manage that image."
	case errRequestConflict:
		code = "request_conflict"
		next = "Reuse this ID only with its original target and image. Create a fresh ID only for a new explicitly authorized deployment."
	case errBadMAC:
		code = "invalid_mac"
		next = "Use the target NIC's six-byte MAC, for example 52:54:00:12:34:56."
	case errBadManifest:
		code = "unknown_profile"
		next = "Call profiles_list and choose a permitted name."
	case errNoImageBase:
		code = "missing_image_base"
		next = "Host the Windows boot files, then ask an admin to set image_base with server_configure."
	case saveFail:
		code = "state_save_failed"
		next = "Check state-directory permissions and disk space before retrying."
		retry = true
	case degradedErr:
		code = "server_degraded"
		next = "An administrator must inspect the state file and restart the server before any deployment."
	}
	return toolError(code, err.Error(), next, retry)
}

func (s *Server) mcpState(p principal) map[string]any {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://localhost/api/state", nil)
	s.getState(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
	var state map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &state)
	for _, key := range []string{"armed", "deployments"} {
		filtered := []any{}
		if rows, ok := state[key].([]any); ok {
			for _, row := range rows {
				m := row.(map[string]any)
				if p.allows(m["manifest"].(string)) {
					filtered = append(filtered, row)
				}
			}
		}
		state[key] = filtered
	}
	return state
}

func (s *Server) mcpDeployment(p principal, raw, run string) toolResult {
	mac, ok := normMAC(raw)
	if !ok {
		return deploymentToolError(errBadMAC)
	}
	st := s.mcpState(p)
	result := map[string]any{"mac": mac, "terminal": false, "stale": false, "poll_after_seconds": 3}
	found := false
	for _, row := range st["armed"].([]any) {
		m := row.(map[string]any)
		if m["mac"] == mac && (run == "" || m["run_id"] == run) {
			result["armed"] = m
			result["next_steps"] = []string{"Network-boot the authorized target. Arming alone does not reboot it."}
			found = true
		}
	}
	for _, row := range st["deployments"].([]any) {
		m := row.(map[string]any)
		if m["mac"] != mac || (run != "" && m["run_id"] != run) {
			continue
		}
		found = true
		result["deployment"] = m
		updated, _ := time.Parse(time.RFC3339Nano, m["updatedAt"].(string))
		age := s.Now().Sub(updated).Seconds()
		if age < 0 {
			age = 0
		}
		result["report_age_seconds"] = age
		status := m["status"].(string)
		terminal := status == "done" || status == "failed" || status == "abandoned"
		if status == "abandoned" {
			result["outcome_unknown"] = true
		}
		result["terminal"] = terminal
		result["stale"] = !terminal && age > 120
		message, _ := m["message"].(string)
		result["next_steps"] = deploymentGuidance(status, message, age)
	}
	if !found {
		if run != "" {
			return toolError("run_not_current", "The requested run is no longer visible for this MAC; it may have been disarmed, dropped at restart or superseded.", "Inspect deployments_list and retained arm_receipt; do not infer success or rearm automatically.", false)
		}
		return toolError("target_not_found", "No visible arm or deployment for this MAC.", "Check the MAC, your image scope and deployments_list. Do not assume the target was installed.", false)
	}
	if _, pending := result["armed"]; pending {
		result["terminal"] = false
		result["next_steps"] = []string{"A new arm is pending. Network-boot the authorized target; the deployment field, if present, is its previous run."}
	}
	return toolOK(result)
}
func deploymentGuidance(status, message string, age float64) []string {
	if status == "abandoned" {
		return []string{"An administrator closed tracking after stopping the target externally. The actual installation outcome is unknown. Verify the target before a fresh, explicitly authorized deployment."}
	}
	if status == "done" {
		return []string{"The target reported completion. Use its console or SSH to verify the installed system; a board report alone is not hardware verification."}
	}
	if status == "failed" {
		m := strings.ToLower(message)
		switch {
		case strings.Contains(m, "hash") || strings.Contains(m, "sha") || strings.Contains(m, "checksum"):
			return []string{"Check the source publisher hash and hosted bytes. Do not re-arm until corrected; the disk may already be partitioned."}
		case strings.Contains(m, "network") || strings.Contains(m, "download") || strings.Contains(m, "tls") || strings.Contains(m, "http"):
			return []string{"Check target networking, NIC drivers, DNS, TLS trust and source reachability. Inspect target logs before re-arming."}
		case strings.Contains(m, "disk"):
			return []string{"Inspect target disk inventory and guard failure. USB, mounted disks and active holders are intentionally excluded. Do not bypass the guard."}
		default:
			return []string{"Read the target console and installer logs. Correct the reported failure before explicitly re-arming; no automatic retry occurs."}
		}
	}
	if age > 120 {
		return []string{"Reports are stale; success is unknown. Inspect the target console, networking and installer logs. Linux first boot and large image downloads can be quiet; do not re-arm a live installer."}
	}
	return []string{"Deployment is in progress. Poll deployment_get in a few seconds. Disarming now cannot cancel an installer that already booted."}
}

func (s *Server) idleError(profile string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	for _, a := range s.st.Armed {
		if profile == "" || a.Manifest == profile {
			return fmt.Errorf("pending arm %s uses %s; disarm it before changing configuration/media", a.MAC, a.Manifest)
		}
	}
	for _, d := range s.st.Deployments {
		if (profile == "" || d.Manifest == profile) && (d.Status == "booting" || d.Status == "running") {
			return fmt.Errorf("deployment %s is still %s; inspect its console and wait for a terminal report before changing configuration/media", d.MAC, d.Status)
		}
	}
	return nil
}

func (s *Server) mcpSetupStatus(p principal) toolResult {
	blockers := []string{}
	if s.ImageBase == "" {
		blockers = append(blockers, "Windows needs a hosted boot-file image_base.")
	}
	if s.LinuxAssets == "" {
		blockers = append(blockers, "Linux needs a configured asset directory and pinned media.")
	}
	urls := lanURLs(s.MCP.Bound)
	if len(urls) == 0 {
		blockers = append(blockers, "HTTP is loopback-only. Remote targets cannot reach boot/config/report routes. Restart with a LAN listener and token.")
	}
	state := s.mcpState(p)
	if state["degraded"] == true {
		blockers = append(blockers, "State persistence is degraded; inspect and restart before arming.")
	}
	pxe := map[string]any{"running": s.MCP.PXE != nil, "directory": s.MCP.PXEDir}
	if x := s.MCP.PXE; x != nil {
		pxe["dhcp_port"] = x.DHCPAddr().Port
		pxe["proxy_port"] = x.ProxyAddr().Port
		pxe["tftp_port"] = x.TFTPAddr().Port
		pxe["boot_files"] = checkBootFiles(x.cfg)
	}
	return toolOK(map[string]any{"identity": p, "image_base": s.ImageBase, "linux_assets": s.LinuxAssets, "manifest_directory": s.ManifestDir, "media_directory": s.MCP.MediaDir, "state_path": s.StatePath, "arm_ttl_seconds": s.ArmTTL.Seconds(), "target_urls": urls, "pxe": pxe, "blockers": blockers, "state": state, "next_tool": "setup_guide", "limits": []string{"MCP controls minute2win; hardware power/firmware and target-console actions require a separate console or power tool.", "Windows ADK installation, WinPE building and external HTTPS hosting are guided host tasks.", "Image-base, Linux-root and TTL settings persist when -data is configured; explicit startup flags override saved values. PXE start/stop is process-local."}})
}
func (s *Server) mcpConfigure(a map[string]json.RawMessage) toolResult {
	if err := s.idleError(""); err != nil {
		return toolError("host_busy", err.Error(), "Disarm pending targets and wait for active installers to finish; configuration changes cannot cancel them.", false)
	}
	base := s.ImageBase
	ttl := s.ArmTTL
	assets := s.LinuxAssets
	if a["image_base"] != nil {
		base = argString(a, "image_base")
		if base != "" {
			if err := checkImageBase(base); err != nil {
				return deploymentToolError(err)
			}
			u, _ := url.Parse(base)
			if u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(base, "\r\n \t") {
				return deploymentToolError(fmt.Errorf("image_base must not contain credentials, query, fragment or whitespace"))
			}
		}
	}
	if a["arm_ttl_seconds"] != nil {
		n := argInt(a, "arm_ttl_seconds", 0)
		if n < 60 || n > 86400 {
			return deploymentToolError(fmt.Errorf("arm_ttl_seconds must be 60 to 86400"))
		}
		ttl = time.Duration(n) * time.Second
	}
	if a["use_media_for_linux"] != nil {
		assets = ""
		if argBool(a, "use_media_for_linux") {
			assets = s.MCP.MediaDir
		}
	}
	if err := writeMCPSettings(s.MCP.SettingsPath, mcpSettings{base, assets, int64(ttl.Seconds())}); err != nil {
		return toolError("config_save_failed", err.Error(), "Check data-directory permissions and free space; the running configuration was not changed.", true)
	}
	s.ImageBase, s.LinuxAssets, s.ArmTTL = base, assets, ttl
	return toolOK(map[string]any{"image_base": base, "linux_assets": assets, "arm_ttl_seconds": ttl.Seconds(), "persistent": s.MCP.SettingsPath != "", "startup_flags_override_saved_settings": true, "startup_flags": []string{"-image-base=" + base, "-linux-assets=" + assets, "-arm-ttl=" + ttl.String()}})
}

func (s *Server) mcpPXE(name string, a map[string]json.RawMessage) toolResult {
	if name == "pxe_stop" {
		if err := s.idleError(""); err != nil {
			return toolError("host_busy", err.Error(), "Wait for deployments and disarm pending machines first.", false)
		}
		if s.MCP.PXE != nil {
			s.MCP.PXE.Close()
			s.MCP.PXE = nil
		}
		return toolOK(map[string]any{"running": false})
	}
	if s.MCP.PXE != nil {
		return toolError("pxe_already_running", "PXE is already running.", "Use setup_status; stop it while idle before changing settings.", false)
	}
	cfg := PXEConfig{Dir: s.MCP.PXEDir, ClientPort: 68, FileX64: "ipxe.efi", FileBIOS: "undionly.kpxe", FileARM64: "ipxe-arm64.efi"}
	for key, dst := range map[string]*string{"file_x64": &cfg.FileX64, "file_bios": &cfg.FileBIOS, "file_arm64": &cfg.FileARM64, "file_ia32": &cfg.FileIA32} {
		if a[key] != nil {
			*dst = argString(a, key)
		}
		if *dst != "" && !safeMediaName(*dst) {
			return deploymentToolError(fmt.Errorf("%s must be a plain filename", key))
		}
	}
	for _, v := range []struct {
		k   string
		def int64
		dst *int
	}{{"dhcp_port", 67, &cfg.DHCPPort}, {"proxy_port", 4011, &cfg.ProxyPort}, {"tftp_port", 69, &cfg.TFTPPort}} {
		n := argInt(a, v.k, v.def)
		if n < 1 || n > 65535 {
			return deploymentToolError(fmt.Errorf("%s must be 1 to 65535", v.k))
		}
		*v.dst = int(n)
	}
	var ip net.IP
	if raw := argString(a, "advertise_ip"); raw != "" {
		ip = net.ParseIP(raw)
		if ip == nil {
			return deploymentToolError(fmt.Errorf("advertise_ip is not an IP address"))
		}
	}
	_, local := localIPv4s()
	port, adv, warning, err := pxeHTTPTarget(s.MCP.Bound, ip, local)
	if err != nil {
		return deploymentToolError(err)
	}
	cfg.HTTPPort, cfg.AdvertiseIP = port, adv
	p, err := s.StartPXE(cfg)
	if err != nil {
		return toolError("pxe_start_failed", err.Error(), "Check listener reachability, UDP port conflicts, bind privileges and setup_guide.", true)
	}
	s.MCP.PXE = p
	return toolOK(map[string]any{"running": true, "warning": warning, "boot_files": checkBootFiles(cfg)})
}

func (s *Server) mcpValidate(ctx context.Context, name string, probe bool) toolResult {
	lp, err := readProfile(filepath.Join(s.ManifestDir, name+".json"), name)
	if err != nil {
		return deploymentToolError(err)
	}
	if lp != nil {
		if err := s.checkLinuxAssets(lp); err != nil {
			return toolError("media_invalid", err.Error(), "Use media_list and media_inspect, then acquire the matching files and update pins from verified publisher metadata.", false)
		}
		return toolOK(map[string]any{"valid": true, "installer": lp.Installer, "assets_verified": true, "target_reachability_verified": false})
	}
	if s.ImageBase == "" {
		return deploymentToolError(errNoImageBase)
	}
	checks := []any{}
	if probe {
		b, err := os.ReadFile(filepath.Join(s.ManifestDir, name+".json"))
		if err != nil {
			return fileToolError(err)
		}
		var p struct {
			Image struct {
				URI string `json:"uri"`
			}
		}
		_ = json.Unmarshal(b, &p)
		urls := []string{p.Image.URI}
		for _, f := range []string{"wimboot", "BCD", "boot.sdi", "boot.wim", "install.ps1", "minute2win/VERSION", "minute2win/Minute2Win.psm1", "minute2win/Start-Minute2Win.ps1", "minute2win/Start-FirstBoot.ps1", "minute2win/unattend-oobe.xml"} {
			urls = append(urls, strings.TrimRight(s.ImageBase, "/")+"/"+f)
		}
		for _, u := range urls {
			check := probeMedia(ctx, u)
			checks = append(checks, check)
			if check["ok"] != true {
				return toolErrorWithChecks(checks)
			}
		}
	}
	return toolOK(map[string]any{"valid": true, "installer": "windows", "checks": checks, "image_hash_verified": false, "target_reachability_verified": false, "next_steps": []string{"The engine validates size, disk policy and image contents in WinPE. Its image SHA-256 check occurs after partitioning; host HEAD checks do not verify bytes or WinPE TLS trust."}})
}
func probeMedia(ctx context.Context, raw string) map[string]any {
	result := map[string]any{"url": redactURL(raw), "ok": false}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "HEAD", raw, nil)
	if err != nil {
		result["error"] = "invalid URL"
		return result
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: safeMediaRedirect}
	res, err := client.Do(req)
	if err != nil {
		result["error"] = "Connection or TLS failure; check host, certificate, DNS and network access."
		return result
	}
	res.Body.Close()
	result["http_status"] = res.StatusCode
	result["size_bytes"] = res.ContentLength
	result["ok"] = res.StatusCode >= 200 && res.StatusCode < 300
	if result["ok"] != true {
		result["error"] = "Host refused HEAD or file is unavailable. 401/403: access policy; 404: path; 405: host must support HEAD for engine image size discovery."
	}
	return result
}
func toolErrorWithChecks(checks []any) toolResult {
	r := toolError("media_unreachable", "A hosted file failed preflight.", "Review checks, fix hosting/auth/path/TLS, and retry profile_validate. Target access and trust still need a boot test.", true)
	r.StructuredContent["checks"] = checks
	b, _ := json.Marshal(r.StructuredContent)
	r.Content[0]["text"] = string(b)
	return r
}
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "invalid URL"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
func safeMediaRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("too many redirects")
	}
	if req.URL.Scheme != "https" || req.URL.User != nil {
		return fmt.Errorf("redirect must be HTTPS without embedded credentials")
	}
	return nil
}

// Keep deterministic ordering in management responses.
func sortedKeys[V any](m map[string]V) []string {
	r := make([]string, 0, len(m))
	for k := range m {
		r = append(r, k)
	}
	sort.Strings(r)
	return r
}
