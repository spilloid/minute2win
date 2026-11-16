package main

import (
	"encoding/json"
	"strings"
)

func (s *Server) mcpAbandon(p principal, a map[string]json.RawMessage) toolResult {
	if !argBool(a, "target_stopped") {
		return toolError("target_not_stopped", "Tracking cannot be closed until the installer has been stopped externally.", "Inspect and stop the target using its console/power tool. This operation cannot cancel it.", false)
	}
	mac, ok := normMAC(argString(a, "mac"))
	if !ok {
		return deploymentToolError(errBadMAC)
	}
	reason := strings.TrimSpace(argString(a, "reason"))
	if reason == "" || len(reason) > 512 {
		return toolError("invalid_reason", "A reason of 1 to 512 bytes is required.", "Describe why the stopped target's tracking must be closed; do not include secrets.", false)
	}
	s.mu.Lock()
	previous := s.st.Deployments[mac]
	if previous == nil {
		s.mu.Unlock()
		return toolError("target_not_found", "No deployment exists for this MAC.", "Use deployments_list.", false)
	}
	if previous.Status == "done" || previous.Status == "failed" || previous.Status == "abandoned" {
		s.mu.Unlock()
		return toolOK(map[string]any{"mac": mac, "already_terminal": true})
	}
	closed := *previous
	closed.Status = "abandoned"
	closed.Phase = "tracking_closed"
	closed.Message = "Tracking closed by " + p.Username + ": " + reason + "; target outcome unknown"
	closed.Key = ""
	closed.Linux = nil
	closed.UpdatedAt = s.Now()
	s.st.Deployments[mac] = &closed
	err := s.commit(func() { s.st.Deployments[mac] = previous })
	s.mu.Unlock()
	if err != nil {
		return deploymentToolError(err)
	}
	s.audit(p, "deployment_abandon", mac, closed.Manifest, "outcome_unknown")
	return toolOK(map[string]any{"mac": mac, "run_id": closed.RunID, "tracking_closed": true, "outcome_unknown": true, "target_was_not_stopped_by_this_tool": true})
}
