package main

import "net/http"

// Credential-free tray telemetry contains counts only, never target identity,
// profile, message, boot ticket or report capability.
func (s *Server) publicStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	active := 0
	for _, d := range s.st.Deployments {
		if d.Status == "booting" || d.Status == "running" {
			active++
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"armedCount": len(s.st.Armed), "deployingCount": active, "degraded": s.degraded})
}
