package main

import (
	"io"
	"net/url"
	"strings"
	"testing"
)

func (f *fixture) form(t *testing.T, path string, v url.Values) string {
	t.Helper()
	r, err := f.ts.Client().PostForm(f.ts.URL+path, v)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 {
		t.Fatalf("%s: status %d", path, r.StatusCode)
	}
	return string(b)
}

func TestUIArmAndDisarm(t *testing.T) {
	f := newFixture(t, "")
	out := f.form(t, "/ui/arm", url.Values{"mac": {"52:54:00:12:34:56"}, "manifest": {"win11-pro"}})
	if !strings.Contains(out, `class="ok"`) || !strings.Contains(out, `hx-swap-oob="true"`) {
		t.Fatalf("arm fragment: %s", out)
	}
	if _, armed := f.do(t, "GET", "/ui/armed", "", ""); !strings.Contains(armed, "52:54:00:12:34:56") {
		t.Fatalf("armed fragment: %s", armed)
	}
	f.form(t, "/ui/disarm", url.Values{"mac": {"52:54:00:12:34:56"}})
	if _, armed := f.do(t, "GET", "/ui/armed", "", ""); !strings.Contains(armed, "Nothing armed") {
		t.Fatalf("still armed: %s", armed)
	}
	if out := f.form(t, "/ui/disarm", url.Values{"mac": {"52:54:00:12:34:56"}}); !strings.Contains(out, "was not armed") || !strings.Contains(out, `class="err"`) {
		t.Fatalf("disarming nothing must not look like success: %s", out)
	}
}

func TestUIRefusalKeepsTheField(t *testing.T) {
	f := newFixture(t, "")
	out := f.form(t, "/ui/arm", url.Values{"mac": {"nope"}, "manifest": {"win11-pro"}})
	if !strings.Contains(out, `class="err"`) || strings.Contains(out, "hx-swap-oob") {
		t.Fatalf("refusal should explain and keep the typed MAC: %s", out)
	}
}

func TestUITokenGuardsArming(t *testing.T) {
	f := newFixture(t, "s3cret")
	if out := f.form(t, "/ui/arm", url.Values{"mac": {"52:54:00:12:34:56"}, "manifest": {"win11-pro"}}); !strings.Contains(out, "token required") {
		t.Fatalf("no token: %s", out)
	}
	if len(f.s.st.Armed) != 0 {
		t.Fatal("armed without a token")
	}
	f.form(t, "/ui/arm", url.Values{"mac": {"52:54:00:12:34:56"}, "manifest": {"win11-pro"}, "token": {"s3cret"}})
	if out := f.form(t, "/ui/disarm", url.Values{"mac": {"52:54:00:12:34:56"}, "token": {"bad"}}); !strings.Contains(out, "token required") || len(f.s.st.Armed) != 1 {
		t.Fatalf("disarmed with a bad token: %s", out)
	}
	if _, page := f.do(t, "GET", "/", "", ""); !strings.Contains(page, `name="token"`) {
		t.Fatal("token field missing when a token is required")
	}
}

func TestUIEscapesTargetText(t *testing.T) {
	f := newFixture(t, "")
	f.arm(t, "52:54:00:12:34:56")
	key := f.bootAndKey(t, "52:54:00:12:34:56")
	f.do(t, "POST", "/api/report", `{"key":"`+key+`","macs":["52:54:00:12:34:56"],"phase":"<img src=x onerror=alert(1)>","status":"running","model":"<script>alert(1)</script>"}`, "")
	_, out := f.do(t, "GET", "/ui/deployments", "", "")
	if strings.Contains(out, "<script>") || strings.Contains(out, "<img") || !strings.Contains(out, "&lt;script&gt;") {
		t.Fatalf("unescaped target text: %s", out)
	}
}

func TestIndexHasNoTokenFieldWithoutToken(t *testing.T) {
	f := newFixture(t, "")
	_, page := f.do(t, "GET", "/", "", "")
	if strings.Contains(page, `name="token"`) || !strings.Contains(page, `<option value="win11-pro">`) {
		t.Fatalf("index: %s", page)
	}
}
