package main

// Use a maintained OIDC verifier for issuer/audience/expiry/JWKS validation;
// do not implement JWT signature handling in the deployment server.
import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type oidcConfig struct {
	Issuer, ClientID, ClientSecret, PublicURL string
	AdminGroup, OperatorGroup                 string
	OperatorProfiles                          []string
}
type oidcFlow struct {
	State, Nonce, Verifier string
	Expires                time.Time
}
type oidcAuth struct {
	cfg      oidcConfig
	mu       sync.Mutex
	provider *oidc.Provider
	oauth    *oauth2.Config
	flows    map[string]oidcFlow
	// Tests inject a TLS-trusting client; production always uses verified TLS.
	client *http.Client
}

func (a *oidcAuth) canonicalHost(r *http.Request) bool {
	public, err := url.Parse(a.cfg.PublicURL)
	if err != nil {
		return false
	}
	requested, err := url.Parse("https://" + r.Host)
	if err != nil {
		return false
	}
	port := func(u *url.URL) string {
		if u.Port() == "" {
			return "443"
		}
		return u.Port()
	}
	return strings.EqualFold(public.Hostname(), requested.Hostname()) && port(public) == port(requested)
}

func oidcFromEnv() (*oidcAuth, error) {
	c := oidcConfig{Issuer: os.Getenv("M2W_OIDC_ISSUER"), ClientID: os.Getenv("M2W_OIDC_CLIENT_ID"), ClientSecret: os.Getenv("M2W_OIDC_CLIENT_SECRET"), PublicURL: strings.TrimRight(os.Getenv("M2W_OIDC_PUBLIC_URL"), "/"), AdminGroup: os.Getenv("M2W_OIDC_ADMIN_GROUP"), OperatorGroup: os.Getenv("M2W_OIDC_OPERATOR_GROUP")}
	if c.Issuer == "" {
		if c.ClientID != "" || c.ClientSecret != "" || c.PublicURL != "" {
			return nil, fmt.Errorf("OIDC configuration is incomplete: M2W_OIDC_ISSUER is required")
		}
		return nil, nil
	}
	if c.ClientID == "" {
		return nil, fmt.Errorf("M2W_OIDC_CLIENT_ID is required")
	}
	for name, raw := range map[string]string{"issuer": c.Issuer, "public URL": c.PublicURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("OIDC %s must be an HTTPS URL without credentials, query or fragment", name)
		}
		if name == "public URL" && u.Path != "" {
			return nil, fmt.Errorf("M2W_OIDC_PUBLIC_URL must be an origin without a path")
		}
	}
	if grants := os.Getenv("M2W_OIDC_OPERATOR_PROFILES"); grants != "" {
		for _, g := range strings.Split(grants, ",") {
			g = strings.TrimSpace(g)
			if g != "*" && !nameRe.MatchString(g) {
				return nil, fmt.Errorf("invalid M2W_OIDC_OPERATOR_PROFILES entry")
			}
			c.OperatorProfiles = append(c.OperatorProfiles, g)
		}
	}
	return &oidcAuth{cfg: c, flows: map[string]oidcFlow{}}, nil
}
func (a *oidcAuth) requestContext(ctx context.Context) context.Context {
	client := a.client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: safeMediaRedirect}
	}
	return context.WithValue(ctx, oauth2.HTTPClient, client)
}
func (a *oidcAuth) setup(ctx context.Context) (*oidc.Provider, *oauth2.Config, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.provider != nil {
		return a.provider, a.oauth, nil
	}
	provider, err := oidc.NewProvider(a.requestContext(ctx), a.cfg.Issuer)
	if err != nil {
		return nil, nil, err
	}
	endpoint := provider.Endpoint()
	if a.cfg.ClientSecret == "" {
		endpoint.AuthStyle = oauth2.AuthStyleInParams
	}
	for _, raw := range []string{endpoint.AuthURL, endpoint.TokenURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" || u.User != nil || u.Scheme != "https" {
			return nil, nil, fmt.Errorf("OIDC endpoints must use HTTPS")
		}
	}
	a.provider = provider
	a.oauth = &oauth2.Config{ClientID: a.cfg.ClientID, ClientSecret: a.cfg.ClientSecret, Endpoint: endpoint, RedirectURL: a.cfg.PublicURL + "/auth/callback", Scopes: []string{oidc.ScopeOpenID, "profile", "email", "groups"}}
	return a.provider, a.oauth, nil
}
func (s *Server) oidcLogin(w http.ResponseWriter, r *http.Request) {
	a := s.Auth.OIDC
	if a == nil {
		http.NotFound(w, r)
		return
	}
	if !a.canonicalHost(r) {
		http.Redirect(w, r, a.cfg.PublicURL+"/auth/login", http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	_, oauth, err := a.setup(ctx)
	if err != nil {
		http.Error(w, "SSO discovery failed. Check the issuer, DNS, certificate and IdP access policy; local sign-in is still available.", http.StatusBadGateway)
		return
	}
	flow := oidcFlow{State: randomID(), Nonce: randomID(), Verifier: oauth2.GenerateVerifier(), Expires: time.Now().Add(10 * time.Minute)}
	browser := randomID()
	a.mu.Lock()
	for k, f := range a.flows {
		if time.Now().After(f.Expires) {
			delete(a.flows, k)
		}
	}
	if len(a.flows) >= 1024 {
		a.mu.Unlock()
		http.Error(w, "Too many pending SSO logins; retry later.", http.StatusTooManyRequests)
		return
	}
	a.flows[hashBytes([]byte(browser))] = flow
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "m2w_oidc_flow", Value: browser, Path: "/auth/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, oauth.AuthCodeURL(flow.State, oauth2.S256ChallengeOption(flow.Verifier), oidc.Nonce(flow.Nonce)), http.StatusFound)
}
func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	a := s.Auth.OIDC
	if a == nil {
		http.NotFound(w, r)
		return
	}
	if !a.canonicalHost(r) {
		http.Error(w, "Use the configured HTTPS console address and start SSO sign-in again.", http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	cookie, err := r.Cookie("m2w_oidc_flow")
	if err != nil {
		http.Error(w, "SSO login cookie is missing; start sign-in again.", http.StatusBadRequest)
		return
	}
	key := hashBytes([]byte(cookie.Value))
	a.mu.Lock()
	flow, ok := a.flows[key]
	delete(a.flows, key)
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "m2w_oidc_flow", Value: "", Path: "/auth/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	if !ok || time.Now().After(flow.Expires) || !tokenEqual(r.URL.Query().Get("state"), flow.State) || r.URL.Query().Get("code") == "" {
		http.Error(w, "SSO state is invalid or expired; start sign-in again.", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(a.requestContext(r.Context()), 15*time.Second)
	defer cancel()
	provider, oauth, err := a.setup(ctx)
	if err != nil {
		http.Error(w, "SSO provider unavailable; retry sign-in.", http.StatusBadGateway)
		return
	}
	token, err := oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		http.Error(w, "SSO code exchange failed. Check the provider grant, client, PKCE and callback URI.", http.StatusBadGateway)
		return
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "SSO provider omitted id_token; request the openid scope.", http.StatusBadGateway)
		return
	}
	id, err := provider.Verifier(&oidc.Config{ClientID: a.cfg.ClientID}).Verify(ctx, raw)
	if err != nil || !tokenEqual(id.Nonce, flow.Nonce) || id.Subject == "" {
		http.Error(w, "SSO identity validation failed; check issuer, audience, signature, expiry and nonce.", http.StatusUnauthorized)
		return
	}
	var claims struct {
		Groups []string `json:"groups"`
	}
	if err := id.Claims(&claims); err != nil {
		http.Error(w, "SSO groups claim must be an array of names.", http.StatusUnauthorized)
		return
	}
	p := principal{Username: "oidc:" + id.Subject, Role: "viewer", Profiles: []string{"*"}}
	for _, g := range claims.Groups {
		if a.cfg.OperatorGroup != "" && g == a.cfg.OperatorGroup {
			p.Role = "operator"
			p.Profiles = append([]string{}, a.cfg.OperatorProfiles...)
		}
	}
	for _, g := range claims.Groups {
		if a.cfg.AdminGroup != "" && g == a.cfg.AdminGroup {
			p.Role = "admin"
			p.Profiles = []string{"*"}
		}
	}
	store := s.Auth
	store.mu.Lock()
	version := ""
	mapped := false
	for _, u := range store.users {
		if u.OIDCSubject == id.Subject {
			p = u.principal
			version = u.Version
			mapped = true
			break
		}
	}
	now := time.Now()
	for k, v := range store.sessions {
		if now.After(v.Expires) {
			delete(store.sessions, k)
		}
	}
	if len(store.sessions) >= 1024 {
		store.mu.Unlock()
		http.Error(w, "Session capacity reached; retry later.", http.StatusServiceUnavailable)
		return
	}
	ticket := randomID()
	expiry := now.Add(time.Hour)
	if id.Expiry.Before(expiry) {
		expiry = id.Expiry
	}
	store.sessions[hashBytes([]byte(ticket))] = authSession{Username: p.Username, Version: version, Expires: expiry, SSO: &p, OIDCSubject: id.Subject, Mapped: mapped}
	store.mu.Unlock()
	// Lax permits the top-level redirect back from the IdP to carry the new
	// session. State/nonce/PKCE bind login; cross-origin mutations are refused.
	http.SetCookie(w, &http.Cookie{Name: "m2w_session", Value: ticket, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: int(time.Until(expiry).Seconds())})
	s.audit(p, "sso_login", "", "", "signed_in")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
