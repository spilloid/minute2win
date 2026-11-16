package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOIDCPKCEIdentityValidationAndRBAC(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var provider *httptest.Server
	nonce, challenge, mode := "", "", "good"
	groups := []string{"authentik Admins"}
	provider = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeJSON(w, map[string]any{"issuer": provider.URL, "authorization_endpoint": provider.URL + "/authorize", "token_endpoint": provider.URL + "/token", "jwks_uri": provider.URL + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			writeJSON(w, map[string]any{"keys": []any{map[string]any{"kty": "RSA", "use": "sig", "kid": "test-key", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		case "/token":
			_ = r.ParseForm()
			if r.PostForm.Get("client_id") != "minute2win-test" || r.Header.Get("Authorization") != "" {
				http.Error(w, "public client must authenticate by client_id plus PKCE, without Basic credentials", 400)
				return
			}
			sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
			if challenge == "" || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
				http.Error(w, "PKCE mismatch", 400)
				return
			}
			claims := map[string]any{"iss": provider.URL, "aud": "minute2win-test", "sub": "immutable-123", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": nonce, "groups": groups}
			switch mode {
			case "nonce":
				claims["nonce"] = "wrong"
			case "audience":
				claims["aud"] = "other-client"
			case "issuer":
				claims["iss"] = "https://wrong-issuer.invalid"
			case "expired":
				claims["exp"] = time.Now().Add(-time.Hour).Unix()
			case "groups":
				claims["groups"] = "admin"
			}
			header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"test-key"}`))
			payload := base64.RawURLEncoding.EncodeToString(mustJSON(t, claims))
			unsigned := header + "." + payload
			digest := sha256.Sum256([]byte(unsigned))
			signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
			if err != nil {
				t.Error(err)
				return
			}
			if mode == "signature" {
				signature[0] ^= 1
			}
			jwt := unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
			writeJSON(w, map[string]any{"access_token": "test-access-token", "token_type": "Bearer", "id_token": jwt})
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	f := newMCPFixture(t)
	f.s.Auth.OIDC = &oidcAuth{cfg: oidcConfig{Issuer: provider.URL, ClientID: "minute2win-test", PublicURL: "https://minute2win.test", AdminGroup: "authentik Admins", OperatorGroup: "m2w-operators", OperatorProfiles: []string{"win11-pro"}}, flows: map[string]oidcFlow{}, client: provider.Client()}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	begin := func() (*http.Cookie, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", f.ts.URL+"/auth/login", nil)
		req.Host = "minute2win.test"
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 302 {
			t.Fatalf("login status %d", res.StatusCode)
		}
		location, err := url.Parse(res.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		nonce = location.Query().Get("nonce")
		challenge = location.Query().Get("code_challenge")
		if nonce == "" || challenge == "" || location.Query().Get("code_challenge_method") != "S256" {
			t.Fatal("missing nonce or PKCE")
		}
		cookies := res.Cookies()
		if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
			t.Fatal("unsafe flow cookie")
		}
		return cookies[0], location.Query().Get("state")
	}
	canonicalResponse, err := client.Get(f.ts.URL + "/auth/login")
	if err != nil {
		t.Fatal(err)
	}
	canonicalResponse.Body.Close()
	if canonicalResponse.StatusCode != 303 || canonicalResponse.Header.Get("Location") != "https://minute2win.test/auth/login" || len(canonicalResponse.Cookies()) != 0 {
		t.Fatal("noncanonical SSO must redirect before issuing a cookie")
	}
	finish := func(cookie *http.Cookie, state string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("GET", f.ts.URL+"/auth/callback?code=test-code&state="+url.QueryEscape(state), nil)
		req.Host = "minute2win.test"
		if cookie != nil {
			req.AddCookie(cookie)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	for _, bad := range []string{"nonce", "audience", "issuer", "expired", "signature", "groups"} {
		mode = bad
		cookie, state := begin()
		if res := finish(cookie, state); res.StatusCode != 401 {
			t.Fatalf("bad %s accepted: %d", bad, res.StatusCode)
		}
	}
	mode = "good"
	cookie, state := begin()
	if res := finish(cookie, "wrong-state"); res.StatusCode != 400 {
		t.Fatal("wrong state accepted")
	}
	if res := finish(cookie, state); res.StatusCode != 400 {
		t.Fatal("flow replay accepted")
	}
	_, state = begin()
	if res := finish(nil, state); res.StatusCode != 400 {
		t.Fatal("unbound login accepted")
	}
	for _, tc := range []struct {
		groups  []string
		role    string
		profile string
	}{{[]string{"authentik Admins"}, "admin", "anything"}, {[]string{"m2w-operators"}, "operator", "win11-pro"}, {[]string{}, "viewer", "anything"}} {
		groups = tc.groups
		cookie, state = begin()
		res := finish(cookie, state)
		if res.StatusCode != 303 {
			t.Fatalf("valid login: %d", res.StatusCode)
		}
		var session *http.Cookie
		for _, c := range res.Cookies() {
			if c.Name == "m2w_session" {
				session = c
			}
		}
		if session == nil || !session.Secure || !session.HttpOnly || session.SameSite != http.SameSiteLaxMode {
			t.Fatal("missing secure SSO session")
		}
		req := httptest.NewRequest("GET", "https://minute2win.test/", nil)
		req.AddCookie(session)
		p, ok := f.s.resolvePrincipal(req, "")
		if !ok || p.Role != tc.role || !p.allows(tc.profile) {
			t.Fatalf("bad mapping: %+v", p)
		}
		if tc.role == "operator" && p.allows("other-image") {
			t.Fatal("operator escaped image grants")
		}
	}
	// An immutable subject mapping overrides group-derived administrative access.
	requireToolOK(t, f.adminTool(t, "account_put", map[string]any{"username": "restricted-sso", "oidc_subject": "immutable-123", "role": "operator", "profiles": []string{"win11-pro"}}))
	groups = []string{"authentik Admins"}
	cookie, state = begin()
	res := finish(cookie, state)
	var session *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "m2w_session" {
			session = c
		}
	}
	if session == nil {
		t.Fatal("no mapped session")
	}
	req := httptest.NewRequest("GET", "https://minute2win.test/", nil)
	req.AddCookie(session)
	p, ok := f.s.resolvePrincipal(req, "")
	if !ok || p.Username != "restricted-sso" || p.Role != "operator" || p.allows("other") {
		t.Fatal(p)
	}
	requireToolOK(t, f.adminTool(t, "account_delete", map[string]any{"username": "restricted-sso"}))
	if _, ok := f.s.resolvePrincipal(req, ""); ok {
		t.Fatal("deleted SSO mapping retained its session")
	}
}

func TestOIDCConfigurationFailsClosed(t *testing.T) {
	for _, k := range []string{"M2W_OIDC_ISSUER", "M2W_OIDC_CLIENT_ID", "M2W_OIDC_CLIENT_SECRET", "M2W_OIDC_PUBLIC_URL", "M2W_OIDC_OPERATOR_PROFILES"} {
		t.Setenv(k, "")
	}
	if a, err := oidcFromEnv(); err != nil || a != nil {
		t.Fatal("OIDC should be off when not configured")
	}
	t.Setenv("M2W_OIDC_ISSUER", "http://insecure.invalid/")
	if _, err := oidcFromEnv(); err == nil {
		t.Fatal("insecure/incomplete OIDC accepted")
	}
	t.Setenv("M2W_OIDC_ISSUER", "https://auth.example.invalid/application/o/minute2win/")
	t.Setenv("M2W_OIDC_CLIENT_ID", "minute2win")
	t.Setenv("M2W_OIDC_PUBLIC_URL", "https://minute2win.example.invalid")
	t.Setenv("M2W_OIDC_OPERATOR_PROFILES", "win11-pro,debian")
	a, err := oidcFromEnv()
	if err != nil || len(a.cfg.OperatorProfiles) != 2 {
		t.Fatal(err)
	}
	t.Setenv("M2W_OIDC_OPERATOR_PROFILES", "../escape")
	if _, err := oidcFromEnv(); err == nil {
		t.Fatal("invalid scope accepted")
	}
	if !strings.Contains(setupGuide("windows"), "ADK") {
		t.Fatal("missing Windows setup")
	}
}
