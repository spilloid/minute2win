package main

// The web UI: htmx on the page, HTML fragments from here. html/template escapes everything a target or operator supplied.
// Fragments answer 200 even for a refused action (htmx does not swap error responses); the message says what happened.

import (
	"errors"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

const targetSeconds = 60 // the minute in minute2win

var pages = template.Must(template.ParseFS(webFS, "web/templates/*.html"))

type msg struct{ Kind, Text string }

type lap struct {
	Name    string
	Seconds float64
}

type card struct {
	*Deployment
	Seconds float64
	Under   bool
	Who     string
	Ago     string
	Laps    []lap
}

func render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pages.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) uiIndex(w http.ResponseWriter, r *http.Request) {
	names, err := s.manifests()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	render(w, "index.html", map[string]any{"Manifests": names, "TokenRequired": s.Token != "", "Msg": (*msg)(nil)})
}

func (s *Server) uiArmed(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.expire()
	armed := []Armed{}
	for _, a := range s.st.Armed {
		armed = append(armed, *a)
	}
	s.mu.Unlock()
	sort.Slice(armed, func(i, j int) bool { return armed[i].ArmedAt.After(armed[j].ArmedAt) })
	render(w, "armed", armed)
}

func ago(d time.Duration) string {
	if d < 90*time.Second {
		return fmt.Sprintf("%d s", int(d.Seconds()))
	}
	return fmt.Sprintf("%d min", int(math.Round(d.Minutes())))
}

func (s *Server) uiDeployments(w http.ResponseWriter, r *http.Request) {
	now := s.Now()
	s.mu.Lock()
	cards := []card{}
	for _, d := range s.st.Deployments {
		dc := *d
		c := card{Deployment: &dc, Ago: ago(now.Sub(d.BootedAt)), Who: strings.Trim(d.Model+", "+d.Serial, ", ")}
		// While live, count from the boot record; once finished, show the engine's own measurement.
		if d.Status == "booting" || d.Status == "running" {
			c.Seconds = now.Sub(d.BootedAt).Seconds()
		} else if v, ok := d.Timing["done"]; ok {
			c.Seconds = v
		} else {
			c.Seconds = d.UpdatedAt.Sub(d.BootedAt).Seconds()
		}
		c.Under = c.Seconds <= targetSeconds
		for k, v := range d.Timing {
			c.Laps = append(c.Laps, lap{k, v})
		}
		sort.Slice(c.Laps, func(i, j int) bool { return c.Laps[i].Seconds < c.Laps[j].Seconds })
		cards = append(cards, c)
	}
	s.mu.Unlock()
	sort.Slice(cards, func(i, j int) bool { return cards[i].BootedAt.After(cards[j].BootedAt) })
	render(w, "deployments", cards)
}

func (s *Server) uiTokenOK(r *http.Request) bool {
	return s.Token == "" || tokenEqual(r.PostFormValue("token"), s.Token)
}

func (s *Server) uiArm(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if !s.uiTokenOK(r) {
		render(w, "msg", &msg{"err", "Not armed: token required."})
		return
	}
	a, err := s.doArm(r.PostFormValue("mac"), r.PostFormValue("manifest"))
	if err != nil {
		render(w, "msg", &msg{"err", "Not armed: " + err.Error()})
		return
	}
	w.Header().Set("HX-Trigger", "m2w-changed")
	render(w, "msg", &msg{"ok", fmt.Sprintf("Armed %s for %s. It will be wiped on its next network boot.", a.MAC, a.Manifest)})
	// clear the MAC field out of band, only on success
	_, _ = w.Write([]byte("\n"))
	_ = pages.ExecuteTemplate(w, "mac-oob", nil)
}

func (s *Server) uiDisarm(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if !s.uiTokenOK(r) {
		render(w, "msg", &msg{"err", "Not disarmed: token required."})
		return
	}
	mac, ok := normMAC(r.PostFormValue("mac"))
	if !ok {
		render(w, "msg", &msg{"err", "Not disarmed: bad MAC."})
		return
	}
	removed, err := s.doDisarm(mac)
	w.Header().Set("HX-Trigger", "m2w-changed")
	switch {
	case err != nil:
		render(w, "msg", &msg{"err", "Not disarmed: " + err.Error() + "."})
	case !removed:
		render(w, "msg", &msg{"err", mac + " was not armed. If it already booted, the deployment is under way and was not cancelled; see Deployments."})
	default:
		render(w, "msg", &msg{"ok", "Disarmed " + mac + "."})
	}
}

var errBadMAC = errors.New("MAC must look like 52:54:00:12:34:56")
var errBadManifest = errors.New("unknown manifest")
