package main

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed web
var webFS embed.FS

// Armed: this MAC is wiped on its next network boot. Serving the boot script consumes the arm (one wipe per arm), so a target
// that network-boots first after the deploy reboots into Windows instead of wiping itself again.
type Armed struct {
	MAC      string    `json:"mac"`
	Manifest string    `json:"manifest"`
	ArmedAt  time.Time `json:"armedAt"`
	Expires  time.Time `json:"expires"`
}

// Deployment: one consumed arm, updated by the engine's reports.
type Deployment struct {
	MAC       string             `json:"mac"`
	Manifest  string             `json:"manifest"`
	BootedAt  time.Time          `json:"bootedAt"`
	UpdatedAt time.Time          `json:"updatedAt"`
	Phase     string             `json:"phase"`
	Status    string             `json:"status"` // booting, running, done, failed
	Message   string             `json:"message,omitempty"`
	Model     string             `json:"model,omitempty"`
	Serial    string             `json:"serial,omitempty"`
	Timing    map[string]float64 `json:"timing,omitempty"`
	// Key authenticates the engine's reports. It is only in the manifest the target fetches at boot (served until the first report
	// or manifestWindow, so iPXE can retry) and never shown by /api/state. Knowing a MAC alone does not let anyone forge an outcome.
	Key string `json:"key,omitempty"`
}

type state struct {
	Armed       map[string]*Armed      `json:"armed"`
	Deployments map[string]*Deployment `json:"deployments"`
}

type Server struct {
	ManifestDir string        // *.json deployment profiles
	ImageBase   string        // where wimboot, BCD, boot.sdi, boot.wim live
	StatePath   string        // "" = memory only
	Token       string        // "" = arming needs no token
	ArmTTL      time.Duration // an arm nobody boots expires
	ReportTTL   time.Duration // reports are accepted this long after the boot
	Now         func() time.Time

	mu sync.Mutex
	st state
	// degraded: a save failed and so did restoring the previous state, so the file on disk may not match what operators were told.
	// Fail closed: no boots deploy and nothing can be armed until someone checks the state file and restarts the server.
	degraded bool
}

var macRe = regexp.MustCompile(`^([0-9a-f]{2}:){5}[0-9a-f]{2}$`)
var nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func normMAC(s string) (string, bool) {
	m := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "-", ":"))
	return m, macRe.MatchString(m)
}

func (s *Server) Load() error {
	s.st = state{Armed: map[string]*Armed{}, Deployments: map[string]*Deployment{}}
	if s.StatePath == "" {
		return nil
	}
	b, err := os.ReadFile(s.StatePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &s.st); err != nil {
		return fmt.Errorf("%s: %w", s.StatePath, err)
	}
	// Arms do not survive a restart. They are short-lived by design, and after any failed or doubtful save the file may hold an arm
	// the operator was told did not happen: dropping them all means a restart can only ever mean "re-arm what you still want".
	if n := len(s.st.Armed); n > 0 {
		log.Printf("dropping %d arm(s) found in the state file: arms do not survive a restart, re-arm what you still want", n)
	}
	s.st.Armed = map[string]*Armed{}
	if s.st.Deployments == nil {
		s.st.Deployments = map[string]*Deployment{}
	}
	return nil
}

// save is called with s.mu held. Write-then-rename so a crash never leaves half a file. Callers that change who may be wiped
// must check the error: an arm whose consumption was not saved would come back after a restart.
func (s *Server) save() error {
	if s.StatePath == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	// Flush the file before the platform-specific replacement. Unix also syncs the parent directory; Windows
	// requests a write-through replacement. Load always drops arms, including after uncertain saves or power loss.
	tmp := s.StatePath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return replaceStateFile(tmp, s.StatePath)
}

const manifestWindow = 10 * time.Minute

var saveFail = writeErr("could not save state; nothing changed")
var degradedErr = writeErr("server is degraded: saving state failed and could not be undone, so the state file may be wrong. Nothing will deploy until it is checked and the server restarted")

// commit saves a mutation. On failure it runs undo and saves again, so the file matches the rolled-back memory; if that also fails,
// the server is degraded. Called with s.mu held.
func (s *Server) commit(undo func()) error {
	if err := s.save(); err == nil {
		return nil
	} else {
		log.Printf("save failed: %v", err)
	}
	undo()
	if err := s.save(); err != nil {
		s.degraded = true
		log.Printf("DEGRADED: restoring the state file also failed (%v); refusing all boots and arms. Check %s, then restart.", err, s.StatePath)
		return degradedErr
	}
	return saveFail
}

type writeErr string

func (e writeErr) Error() string { return string(e) }

func (s *Server) expire() {
	now := s.Now()
	for k, a := range s.st.Armed {
		if now.After(a.Expires) {
			delete(s.st.Armed, k)
		}
	}
}

func (s *Server) manifests() ([]string, error) {
	files, err := filepath.Glob(filepath.Join(s.ManifestDir, "*.json"))
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, f := range files {
		names = append(names, strings.TrimSuffix(filepath.Base(f), ".json"))
	}
	sort.Strings(names)
	return names, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	web, _ := fs.Sub(webFS, "web")
	mux.HandleFunc("GET /{$}", s.uiIndex)
	mux.Handle("GET /static/", http.FileServer(http.FS(web)))
	mux.HandleFunc("GET /ui/armed", s.uiArmed)
	mux.HandleFunc("GET /ui/deployments", s.uiDeployments)
	mux.HandleFunc("POST /ui/arm", s.uiArm)
	mux.HandleFunc("POST /ui/disarm", s.uiDisarm)
	mux.HandleFunc("GET /api/manifests", s.listManifests)
	mux.HandleFunc("GET /api/state", s.getState)
	mux.HandleFunc("POST /api/armed", s.auth(requireJSON(s.arm)))
	mux.HandleFunc("DELETE /api/armed/{mac}", s.auth(s.disarm))
	mux.HandleFunc("GET /boot/{mac}", s.boot)
	mux.HandleFunc("GET /manifest/{mac}", s.manifest)
	mux.HandleFunc("POST /api/report", requireJSON(s.report))
	// Every state-changing route (arm, disarm, the UI forms, reports) refuses cross-origin browser requests, so a page on another
	// site cannot arm a wipe. Requests without browser headers (curl, scripts, the WinPE engine) are not affected.
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "cross-origin request refused: changes are only accepted from this server's own page or from non-browser clients", http.StatusForbidden)
	}))
	return cop.Handler(mux)
}

// requireJSON: JSON mutations must say so. A browser cannot send application/json to another site without a CORS preflight, which
// this server never grants, so this also stops browsers too old to send Origin or Sec-Fetch-Site.
func requireJSON(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
			http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
			return
		}
		h(w, r)
	}
}

// directHost reports whether a request is addressed to an IP address or localhost. With no token, DNS rebinding is how a web page
// could still reach this server as "same-origin", and that needs a host name the attacker controls; an address or localhost
// cannot be rebound.
func directHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	return strings.EqualFold(host, "localhost") || net.ParseIP(host) != nil
}

const rebindRefusal = "without M2W_TOKEN, arming and disarming are only accepted at an IP address or localhost (this blocks DNS-rebinding " +
	"attacks from web pages). Open the console by address, such as http://127.0.0.1:8080/, or set M2W_TOKEN"

// operatorRefusal says why an arm or disarm may not go ahead, or "" if it may. got is the token the request carries.
func (s *Server) operatorRefusal(r *http.Request, got string) (msg string, status int) {
	if s.Token == "" {
		if !directHost(r.Host) {
			return rebindRefusal, http.StatusForbidden
		}
		return "", 0
	}
	if !tokenEqual(got, s.Token) {
		return "token required", http.StatusUnauthorized
	}
	return "", 0
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if msg, code := s.operatorRefusal(r, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")); msg != "" {
			http.Error(w, msg, code)
			return
		}
		h(w, r)
	}
}

func tokenEqual(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) listManifests(w http.ResponseWriter, r *http.Request) {
	names, err := s.manifests()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, names)
}

func (s *Server) getState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	armed := []*Armed{}
	for _, a := range s.st.Armed {
		armed = append(armed, a)
	}
	sort.Slice(armed, func(i, j int) bool { return armed[i].ArmedAt.After(armed[j].ArmedAt) })
	deps := []Deployment{}
	for _, d := range s.st.Deployments {
		v := *d
		v.Key = ""
		deps = append(deps, v)
	}
	sort.Slice(deps, func(i, j int) bool { return deps[i].BootedAt.After(deps[j].BootedAt) })
	writeJSON(w, map[string]any{"armed": armed, "deployments": deps, "now": s.Now(), "tokenRequired": s.Token != "", "degraded": s.degraded})
}

func (s *Server) arm(w http.ResponseWriter, r *http.Request) {
	var req struct{ MAC, Manifest string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	a, err := s.doArm(req.MAC, req.Manifest)
	if err == saveFail || err == degradedErr {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, a)
}

func (s *Server) disarm(w http.ResponseWriter, r *http.Request) {
	mac, _ := normMAC(r.PathValue("mac"))
	removed, err := s.doDisarm(mac)
	switch {
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	case !removed:
		http.Error(w, "not armed (if it already booted, the deployment is under way and was not cancelled)", http.StatusNotFound)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// doArm and doDisarm are shared by the JSON API and the web UI, so both follow the same rules.
func (s *Server) doArm(rawMAC, manifest string) (*Armed, error) {
	mac, ok := normMAC(rawMAC)
	if !ok {
		return nil, errBadMAC
	}
	if !nameRe.MatchString(manifest) {
		return nil, errBadManifest
	}
	if s.ImageBase == "" {
		return nil, errNoImageBase
	}
	if err := checkProfile(filepath.Join(s.ManifestDir, manifest+".json"), manifest); err != nil {
		return nil, err
	}
	now := s.Now()
	a := &Armed{MAC: mac, Manifest: manifest, ArmedAt: now, Expires: now.Add(s.ArmTTL)}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.degraded {
		return nil, degradedErr
	}
	prev := s.st.Armed[mac]
	s.st.Armed[mac] = a
	if err := s.commit(func() {
		if prev != nil {
			s.st.Armed[mac] = prev
		} else {
			delete(s.st.Armed, mac)
		}
	}); err != nil {
		return nil, err
	}
	return a, nil
}

// doDisarm reports whether an arm was removed: false means there was none, possibly because the machine already booted.
func (s *Server) doDisarm(mac string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.st.Armed[mac]
	if a == nil {
		return false, nil
	}
	delete(s.st.Armed, mac)
	if err := s.commit(func() { s.st.Armed[mac] = a }); err != nil {
		return false, err
	}
	return true, nil
}

func origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// iPXE: chain http://<server>/boot/${net0/mac}. Not armed (or expired): exit 1, so the firmware boots its next device, the disk.
func (s *Server) boot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	// Never echo the request into the script: the path is attacker-controlled and iPXE runs every line.
	const notArmed = "#!ipxe\necho minute2win: not armed, booting the local disk\nexit 1\n"
	mac, ok := normMAC(r.PathValue("mac"))
	if !ok {
		fmt.Fprint(w, notArmed)
		return
	}
	s.mu.Lock()
	s.expire()
	a := s.st.Armed[mac]
	if a == nil || s.degraded {
		s.mu.Unlock()
		fmt.Fprint(w, notArmed)
		return
	}
	key := make([]byte, 16)
	_, _ = rand.Read(key)
	prev := s.st.Deployments[mac]
	delete(s.st.Armed, mac)
	now := s.Now()
	s.st.Deployments[mac] = &Deployment{MAC: mac, Manifest: a.Manifest, BootedAt: now, UpdatedAt: now, Phase: "boot", Status: "booting", Key: hex.EncodeToString(key)}
	// Fail closed: if the consumed arm can't be saved, a restart would bring it back, so don't deploy at all.
	if err := s.commit(func() {
		s.st.Armed[mac] = a
		if prev != nil {
			s.st.Deployments[mac] = prev
		} else {
			delete(s.st.Deployments, mac)
		}
	}); err != nil {
		s.mu.Unlock()
		fmt.Fprint(w, "#!ipxe\necho minute2win: server could not save state, not deploying\nexit 1\n")
		return
	}
	s.mu.Unlock()
	base := strings.TrimRight(s.ImageBase, "/")
	fmt.Fprintf(w, "#!ipxe\necho minute2win: %s armed for %s, deploying (this wipes the disk)\n"+
		"kernel %s/wimboot\ninitrd %s/BCD BCD\ninitrd %s/boot.sdi boot.sdi\ninitrd --name manifest.json %s/manifest/%s manifest.json\ninitrd %s/boot.wim boot.wim\nboot\n",
		mac, a.Manifest, base, base, base, origin(r), mac, base)
}

// The armed manifest, with report.url pointing back here so the engine's progress shows on the board.
func (s *Server) manifest(w http.ResponseWriter, r *http.Request) {
	mac, _ := normMAC(r.PathValue("mac"))
	// Served (idempotently, so iPXE can retry) only while the deployment is still booting: the first report or manifestWindow
	// closes it, which keeps the report key from being fetched later by anyone who learns the MAC from the board.
	s.mu.Lock()
	d := s.st.Deployments[mac]
	if d == nil || d.Status != "booting" || s.Now().Sub(d.BootedAt) > manifestWindow {
		s.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	key, name := d.Key, d.Manifest
	s.mu.Unlock()
	b, err := os.ReadFile(filepath.Join(s.ManifestDir, name+".json"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		http.Error(w, "manifest is not valid JSON", http.StatusInternalServerError)
		return
	}
	m["report"] = map[string]any{"url": origin(r) + "/api/report", "key": key}
	writeJSON(w, m)
}

// Reports come from WinPE with no credentials, so they are only accepted for a MAC that booted from here recently.
func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	var rep struct {
		Key     string             `json:"key"`
		MACs    []string           `json:"macs"`
		Phase   string             `json:"phase"`
		Status  string             `json:"status"`
		Message string             `json:"message"`
		Model   string             `json:"model"`
		Serial  string             `json:"serial"`
		Timing  map[string]float64 `json:"timing"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&rep); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	for _, raw := range rep.MACs {
		mac, ok := normMAC(raw)
		d := s.st.Deployments[mac]
		if !ok || d == nil || now.Sub(d.BootedAt) > s.ReportTTL || d.Key == "" || !tokenEqual(rep.Key, d.Key) {
			continue
		}
		if d.Status == "done" || d.Status == "failed" {
			http.Error(w, "deployment already finished", http.StatusConflict)
			return
		}
		d.UpdatedAt, d.Phase, d.Message = now, clip(rep.Phase, 64), clip(rep.Message, 1024)
		d.Status = map[string]string{"done": "done", "failed": "failed"}[rep.Status]
		if d.Status == "" {
			d.Status = "running"
		}
		d.Model, d.Serial, d.Timing = clip(rep.Model, 128), clip(rep.Serial, 128), rep.Timing
		_ = s.save() // the board only; a lost report changes nobody's disk
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Error(w, "no recent boot for these MACs", http.StatusNotFound)
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

var errNoImageBase = writeErr("this server has no -image-base, so it has no boot files to send. Restart it with -image-base set to the URL " +
	"of your own folder holding wimboot, BCD, boot.sdi and boot.wim")

var sha256Re = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// placeholderHost: the documentation's example hosts (RFC 2606/6761 reserved names), which a profile or flag copied from the
// docs still carries until someone replaces it.
func placeholderHost(h string) bool {
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	for _, d := range []string{"example.com", "example.net", "example.org", "example", "invalid"} {
		if h == d || strings.HasSuffix(h, "."+d) {
			return true
		}
	}
	return false
}

// checkProfile refuses, at arm time, a profile that could not deploy: not JSON (a BOM included), or an image URL or hash that is
// missing or still a placeholder. The engine checks the whole manifest again in WinPE; this only stops an arm that is bound to
// fail after it has been consumed, or that would fetch an image from a host the operator never chose.
func checkProfile(path, name string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return errBadManifest
	}
	var p struct {
		Image struct {
			URI    string `json:"uri"`
			SHA256 string `json:"sha256"`
		} `json:"image"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return fmt.Errorf("profile %s is not valid JSON (%v); save it as UTF-8 without a byte-order mark", name, err)
	}
	u, err := url.Parse(p.Image.URI)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return fmt.Errorf("profile %s: image.uri must be the https:// URL of your own Windows image", name)
	}
	if placeholderHost(u.Hostname()) {
		return fmt.Errorf("profile %s: image.uri still points at the placeholder host %s; set your own image URL and its SHA-256", name, u.Hostname())
	}
	if !sha256Re.MatchString(p.Image.SHA256) || strings.Trim(p.Image.SHA256, "0") == "" {
		return fmt.Errorf("profile %s: image.sha256 must be the SHA-256 of your image (64 hex digits, not a placeholder)", name)
	}
	return nil
}
