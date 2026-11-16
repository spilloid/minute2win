package main

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type principal struct {
	Username string   `json:"username"`
	Role     string   `json:"role"`
	Profiles []string `json:"profiles"`
}

var errImageScope = errors.New("arm is outside your image scope")

func (p principal) canOperate() bool { return p.Role == "admin" || p.Role == "operator" }
func (p principal) allows(profile string) bool {
	if p.Role == "admin" {
		return true
	}
	if p.Role == "" {
		return false
	}
	for _, v := range p.Profiles {
		if v == "*" || v == profile {
			return true
		}
	}
	return false
}

type principalKey struct{}

func principalFromContext(ctx context.Context) principal {
	p, _ := ctx.Value(principalKey{}).(principal)
	return p
}

type account struct {
	principal
	OIDCSubject  string   `json:"oidc_subject,omitempty"`
	PasswordHash string   `json:"password_hash,omitempty"`
	TokenHashes  []string `json:"token_hashes,omitempty"`
	Version      string   `json:"version"`
}
type authSession struct {
	Username, Version string
	Expires           time.Time
	SSO               *principal
	OIDCSubject       string
	Mapped            bool
}
type loginLimit struct {
	Attempts int
	Since    time.Time
}
type authStore struct {
	mu          sync.Mutex
	Path        string
	ProxyHeader string
	ProxySecret string
	OIDC        *oidcAuth
	users       map[string]account
	sessions    map[string]authSession
	limits      map[string]loginLimit
}

func newAuthStore(path string) *authStore {
	return &authStore{Path: path, users: map[string]account{}, sessions: map[string]authSession{}, limits: map[string]loginLimit{}}
}
func validProxyHeader(s string) bool {
	if !strings.HasPrefix(strings.ToLower(s), "x-") || strings.EqualFold(s, "X-M2W-Proxy-Secret") || strings.EqualFold(s, "X-Forwarded-Proto") {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return len(s) > 2 && len(s) <= 128
}
func (a *authStore) hasAccounts() bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.users) > 0
}
func (a *authStore) requiresLogin() bool {
	return a != nil && (a.OIDC != nil || a.ProxyHeader != "" || a.hasAccounts())
}
func (a *authStore) load() error {
	if a.Path == "" {
		return nil
	}
	b, err := os.ReadFile(a.Path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var disk struct {
		Accounts []account `json:"accounts"`
	}
	if err := json.Unmarshal(b, &disk); err != nil {
		return fmt.Errorf("auth file: %w", err)
	}
	for _, u := range disk.Accounts {
		if err := validateAccount(u); err != nil {
			return err
		}
		if _, exists := a.users[u.Username]; exists {
			return fmt.Errorf("duplicate account %q", u.Username)
		}
		for _, other := range a.users {
			if u.OIDCSubject != "" && u.OIDCSubject == other.OIDCSubject {
				return fmt.Errorf("duplicate OIDC subject")
			}
		}
		a.users[u.Username] = u
	}
	return nil
}
func validateAccount(u account) error {
	if len(u.Username) < 1 || len(u.Username) > 128 || strings.TrimSpace(u.Username) != u.Username || strings.ContainsAny(u.Username, "\r\n\x00") {
		return fmt.Errorf("username must be 1 to 128 characters without surrounding whitespace or control characters")
	}
	if u.Username == "server-admin" || u.Username == "local-preview" || strings.HasPrefix(u.Username, "oidc:") {
		return fmt.Errorf("reserved account name")
	}
	if u.Role != "viewer" && u.Role != "operator" && u.Role != "admin" {
		return fmt.Errorf("role must be viewer, operator or admin")
	}
	if len(u.Profiles) > 256 {
		return fmt.Errorf("at most 256 profile grants per account")
	}
	for _, p := range u.Profiles {
		if p != "*" && !nameRe.MatchString(p) {
			return fmt.Errorf("invalid profile grant %q", p)
		}
	}
	if u.PasswordHash != "" {
		if _, _, err := parsePasswordHash(u.PasswordHash); err != nil {
			return err
		}
	}
	if u.Version == "" {
		return fmt.Errorf("account version is required")
	}
	if len(u.OIDCSubject) > 512 || strings.ContainsAny(u.OIDCSubject, "\r\n\x00") {
		return fmt.Errorf("invalid OIDC subject")
	}
	for _, h := range u.TokenHashes {
		if !sha256Re.MatchString(h) {
			return fmt.Errorf("invalid token hash")
		}
	}
	return nil
}
func (a *authStore) saveLocked() error {
	if a.Path == "" {
		return fmt.Errorf("account persistence requires -auth-file (or -mcp with a data directory)")
	}
	users := []account{}
	for _, k := range sortedKeys(a.users) {
		users = append(users, a.users[k])
	}
	b, _ := json.MarshalIndent(map[string]any{"accounts": users}, "", "  ")
	root, err := os.OpenRoot(filepath.Dir(a.Path))
	if err != nil {
		return err
	}
	defer root.Close()
	return atomicRootWrite(root, filepath.Base(a.Path), b)
}

const passwordIterations = 600000

func passwordHash(password string) (string, error) {
	var salt [16]byte
	_, _ = rand.Read(salt[:])
	key, err := pbkdf2.Key(sha256.New, password, salt[:], passwordIterations, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%x$%x", passwordIterations, salt, key), nil
}
func parsePasswordHash(encoded string) ([]byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" || parts[1] != strconv.Itoa(passwordIterations) {
		return nil, nil, fmt.Errorf("invalid PBKDF2-SHA256 password hash")
	}
	salt, err := hex.DecodeString(parts[2])
	if err != nil || len(salt) != 16 {
		return nil, nil, fmt.Errorf("invalid password salt")
	}
	key, err := hex.DecodeString(parts[3])
	if err != nil || len(key) != 32 {
		return nil, nil, fmt.Errorf("invalid password hash")
	}
	return salt, key, nil
}
func verifyPassword(encoded, password string) bool {
	salt, want, err := parsePasswordHash(encoded)
	if err != nil {
		salt = make([]byte, 16)
		want = make([]byte, 32)
	}
	got, _ := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	return tokenEqual(string(got), string(want)) && err == nil
}

func (s *Server) resolvePrincipal(r *http.Request, fallback string) (principal, bool) {
	admin := principal{"server-admin", "admin", []string{"*"}}
	bearer := ""
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		bearer = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	if s.Token != "" && (tokenEqual(bearer, s.Token) || tokenEqual(fallback, s.Token)) {
		return admin, true
	}
	a := s.Auth
	if a != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		if bearer != "" {
			h := hashBytes([]byte(bearer))
			for _, u := range a.users {
				for _, v := range u.TokenHashes {
					if tokenEqual(h, v) {
						return u.principal, true
					}
				}
			}
		}
		if a.ProxyHeader != "" && a.ProxySecret != "" && tokenEqual(r.Header.Get("X-M2W-Proxy-Secret"), a.ProxySecret) {
			if u, ok := a.users[r.Header.Get(a.ProxyHeader)]; ok {
				return u.principal, true
			}
		}
		if c, err := r.Cookie("m2w_session"); err == nil {
			key := hashBytes([]byte(c.Value))
			if session, ok := a.sessions[key]; ok && time.Now().Before(session.Expires) {
				if session.SSO != nil {
					if session.Mapped {
						if u, ok := a.users[session.Username]; ok && u.Version == session.Version && u.OIDCSubject == session.OIDCSubject {
							return u.principal, true
						}
						return principal{}, false
					}
					for _, u := range a.users {
						if u.OIDCSubject == session.OIDCSubject {
							return principal{}, false
						}
					}
					return *session.SSO, true
				}
				if session.Username == admin.Username && session.Version == hashBytes([]byte(s.Token)) && s.Token != "" {
					return admin, true
				}
				if u, ok := a.users[session.Username]; ok && u.Version == session.Version {
					return u.principal, true
				}
			}
		}
		if len(a.users) > 0 || a.OIDC != nil || a.ProxyHeader != "" {
			return principal{}, false
		}
	}
	if s.Token == "" && directHost(r.Host) {
		return principal{"local-preview", "admin", []string{"*"}}, true
	}
	return principal{}, false
}

func (s *Server) bearerPrincipal(r *http.Request) (principal, bool) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return principal{}, false
	}
	copy := r.Clone(r.Context())
	copy.Header = make(http.Header)
	copy.Header.Set("Authorization", r.Header.Get("Authorization"))
	if s.Token == "" && !s.Auth.hasAccounts() {
		return principal{}, false
	}
	return s.resolvePrincipal(copy, "")
}

func (s *Server) mayDisarm(p principal, mac string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.st.Armed[mac]
	return a == nil || p.allows(a.Manifest)
}

func (s *Server) uiLogin(w http.ResponseWriter, r *http.Request) {
	if oidc := s.Auth.OIDC; oidc != nil && !oidc.canonicalHost(r) {
		http.Redirect(w, r, oidc.cfg.PublicURL+"/login", http.StatusSeeOther)
		return
	}
	if p, ok := s.resolvePrincipal(r, ""); ok && p.Role != "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	render(w, "login.html", map[string]any{"Msg": "", "LocalAccounts": s.Auth.hasAccounts(), "SSOEnabled": s.Auth.OIDC != nil})
}
func (s *Server) uiLoginPost(w http.ResponseWriter, r *http.Request) {
	if oidc := s.Auth.OIDC; oidc != nil && !oidc.canonicalHost(r) {
		http.Redirect(w, r, oidc.cfg.PublicURL+"/login", http.StatusSeeOther)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if !directHost(r.Host) && s.Token == "" {
		http.Error(w, rebindRefusal, http.StatusForbidden)
		return
	}
	username, password := r.PostFormValue("username"), r.PostFormValue("password")
	remote, _, _ := net.SplitHostPort(r.RemoteAddr)
	a := s.Auth
	a.mu.Lock()
	now := time.Now()
	limit := a.limits[remote]
	if now.Sub(limit.Since) > 15*time.Minute {
		limit = loginLimit{Since: now}
	}
	if limit.Attempts >= 10 {
		a.mu.Unlock()
		http.Error(w, "Too many sign-in attempts; retry in 15 minutes.", http.StatusTooManyRequests)
		return
	}
	limit.Attempts++
	if len(a.limits) < 4096 {
		a.limits[remote] = limit
	} else if _, ok := a.limits[remote]; ok {
		a.limits[remote] = limit
	} else {
		a.mu.Unlock()
		http.Error(w, "Sign-in capacity reached; retry later.", http.StatusTooManyRequests)
		return
	}
	u, exists := a.users[username]
	a.mu.Unlock()
	version := ""
	valid := false
	if username == "server-admin" && s.Token != "" && tokenEqual(password, s.Token) {
		valid = true
		version = hashBytes([]byte(s.Token))
	} else {
		valid = verifyPassword(u.PasswordHash, password) && exists
		version = u.Version
	}
	if !valid {
		w.WriteHeader(http.StatusUnauthorized)
		render(w, "login.html", map[string]any{"Msg": "Sign-in failed. Check your account and password.", "LocalAccounts": a.hasAccounts(), "SSOEnabled": a.OIDC != nil})
		return
	}
	a.mu.Lock()
	if username != "server-admin" {
		current, ok := a.users[username]
		if !ok || current.Version != version {
			a.mu.Unlock()
			http.Error(w, "Account changed; sign in again.", http.StatusUnauthorized)
			return
		}
	}
	for k, v := range a.sessions {
		if now.After(v.Expires) {
			delete(a.sessions, k)
		}
	}
	if len(a.sessions) >= 1024 {
		a.mu.Unlock()
		http.Error(w, "Session capacity reached; retry later.", http.StatusServiceUnavailable)
		return
	}
	ticket := randomID()
	a.sessions[hashBytes([]byte(ticket))] = authSession{Username: username, Version: version, Expires: now.Add(12 * time.Hour)}
	delete(a.limits, remote)
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "m2w_session", Value: ticket, Path: "/", HttpOnly: true, Secure: s.secureRequest(r), SameSite: http.SameSiteStrictMode, MaxAge: 12 * 60 * 60})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (s *Server) secureRequest(r *http.Request) bool {
	return r.TLS != nil || s.Auth.OIDC != nil || (s.Auth.ProxySecret != "" && tokenEqual(r.Header.Get("X-M2W-Proxy-Secret"), s.Auth.ProxySecret) && r.Header.Get("X-Forwarded-Proto") == "https")
}
func (s *Server) uiLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("m2w_session"); err == nil {
		s.Auth.mu.Lock()
		delete(s.Auth.sessions, hashBytes([]byte(c.Value)))
		s.Auth.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "m2w_session", Value: "", Path: "/", HttpOnly: true, Secure: s.secureRequest(r), SameSite: http.SameSiteStrictMode, MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) mcpAccounts(actor principal, action string, a map[string]json.RawMessage) toolResult {
	store := s.Auth
	if store == nil {
		return toolError("auth_unavailable", "Account store is unavailable.", "Configure -auth-file and restart.", false)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if action == "accounts_list" {
		users := []principal{}
		for _, k := range sortedKeys(store.users) {
			users = append(users, store.users[k].principal)
		}
		return toolOK(map[string]any{"accounts": users, "sso_proxy_enabled": store.ProxyHeader != "", "persistent": store.Path != ""})
	}
	username := argString(a, "username")
	old, exists := store.users[username]
	if username == "server-admin" || username == "local-preview" {
		return toolError("reserved_account", "That account name is reserved.", "Choose a different local or SSO identity.", false)
	}
	if action == "account_delete" {
		if username == actor.Username {
			return toolError("cannot_delete_self", "Cannot remove your current identity.", "Sign in as another administrator first.", false)
		}
		if !exists {
			return toolError("account_not_found", "Unknown account.", "Use accounts_list.", false)
		}
		delete(store.users, username)
		if err := store.saveLocked(); err != nil {
			store.users[username] = old
			return fileToolError(err)
		}
		s.audit(actor, "account_delete", "", username, "deleted")
		return toolOK(map[string]any{"deleted": true, "username": username})
	}
	if action == "account_token_create" {
		if !exists {
			return toolError("account_not_found", "Unknown account.", "Create the account or use accounts_list.", false)
		}
		if len(old.TokenHashes) >= 16 {
			return toolError("token_limit", "This account already has 16 tokens.", "Update the account to revoke existing tokens, then issue a new one.", false)
		}
		secret := "m2w_" + randomID() + randomID()
		updated := old
		updated.TokenHashes = append(append([]string{}, old.TokenHashes...), hashBytes([]byte(secret)))
		store.users[username] = updated
		if err := store.saveLocked(); err != nil {
			store.users[username] = old
			return fileToolError(err)
		}
		s.audit(actor, "token_create", "", username, "issued")
		return toolOK(map[string]any{"username": username, "token": secret, "show_once": true, "role": old.Role, "profiles": old.Profiles})
	}
	var grants []string
	_ = json.Unmarshal(a["profiles"], &grants)
	if grants == nil {
		grants = []string{}
	}
	u := account{principal: principal{username, argString(a, "role"), grants}, PasswordHash: old.PasswordHash, OIDCSubject: old.OIDCSubject, Version: randomID()}
	if a["oidc_subject"] != nil {
		u.OIDCSubject = argString(a, "oidc_subject")
	}
	if len(u.OIDCSubject) > 512 || strings.ContainsAny(u.OIDCSubject, "\r\n\x00") {
		return deploymentToolError(fmt.Errorf("invalid oidc_subject"))
	}
	for _, other := range store.users {
		if other.Username != username && u.OIDCSubject != "" && other.OIDCSubject == u.OIDCSubject {
			return toolError("subject_conflict", "OIDC subject is already assigned to another account.", "Update the existing identity instead.", false)
		}
	}
	if a["password"] != nil {
		password := argString(a, "password")
		if len(password) < 12 || len(password) > 1024 {
			return toolError("weak_password", "Local passwords must be 12 to 1024 bytes.", "Choose a unique passphrase, or omit password for an SSO-only identity.", false)
		}
		hash, err := passwordHash(password)
		if err != nil {
			return fileToolError(err)
		}
		u.PasswordHash = hash
	}
	if err := validateAccount(u); err != nil {
		return deploymentToolError(err)
	}
	if actor.Username == username && u.Role != "admin" {
		return toolError("cannot_demote_self", "Cannot demote your current administrative identity.", "Use another administrator to change your role.", false)
	}
	store.users[username] = u
	if err := store.saveLocked(); err != nil {
		if exists {
			store.users[username] = old
		} else {
			delete(store.users, username)
		}
		return fileToolError(err)
	}
	s.audit(actor, "account_put", "", username, "saved")
	return toolOK(map[string]any{"account": u.principal, "sessions_and_tokens_revoked": true})
}

func (s *Server) audit(p principal, action, mac, profile, outcome string) {
	b, _ := json.Marshal(map[string]any{"time": s.Now().UTC(), "actor": p.Username, "role": p.Role, "action": action, "mac": mac, "profile": profile, "outcome": outcome})
	log.Printf("audit %s", b)
}
