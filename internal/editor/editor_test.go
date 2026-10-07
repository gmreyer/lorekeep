package editor

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureRepo is the validator's sound world; tests copy it, since a build
// writes build/ and a save writes world/.
var fixtureRepo = filepath.Join("..", "..", "testdata", "world-ok")

// newTestServer starts an editor over a copy of the fixture, with its
// settings in a temporary folder, and returns it with its repo.
func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	repo := t.TempDir()
	for _, sub := range []string{"schema", "world"} {
		if err := os.CopyFS(filepath.Join(repo, sub), os.DirFS(filepath.Join(fixtureRepo, sub))); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(Config{Repo: repo, Settings: filepath.Join(t.TempDir(), "editor.json")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.host = "127.0.0.1:4321"
	return s, repo
}

// do sends a request as the editor's own page would: the right Host, the
// token cookie, and for anything but GET the token header.
func do(t *testing.T, s *Server, method, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(method, target, body)
	r.Host = s.host
	r.AddCookie(&http.Cookie{Name: s.cookieName(), Value: s.token})
	if method != http.MethodGet {
		r.Header.Set(tokenHeader, s.token)
	}
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

// TestRejectsForeignHostAndMissingToken pins the guard that keeps other web
// pages, and DNS rebinding, from driving the editor (Step 6 review focus 2).
func TestRejectsForeignHostAndMissingToken(t *testing.T) {
	s, _ := newTestServer(t)
	const post = "/settings/keys/reset"

	send := func(method, target, host string, cookie, header bool) int {
		r := httptest.NewRequest(method, target, nil)
		r.Host = host
		if cookie {
			r.AddCookie(&http.Cookie{Name: s.cookieName(), Value: s.token})
		}
		if header {
			r.Header.Set(tokenHeader, s.token)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w.Code
	}

	cases := []struct {
		name           string
		method, target string
		host           string
		cookie, header bool
		want           int
	}{
		{"own page", "GET", "/", s.host, true, false, http.StatusOK},
		{"rebound host", "GET", "/", "evil.example:4321", true, false, http.StatusForbidden},
		{"localhost by name", "GET", "/", "localhost:4321", true, false, http.StatusForbidden},
		{"other port", "GET", "/", "127.0.0.1:9999", true, false, http.StatusForbidden},
		{"page without cookie", "GET", "/", s.host, false, false, http.StatusForbidden},
		{"static without cookie", "GET", "/static/tokens.css", s.host, false, false, http.StatusOK},
		{"static on a foreign host", "GET", "/static/tokens.css", "evil.example:4321", false, false, http.StatusForbidden},
		{"post with both", "POST", post, s.host, true, true, http.StatusOK},
		{"post without header", "POST", post, s.host, true, false, http.StatusForbidden},
		{"post without cookie", "POST", post, s.host, false, true, http.StatusForbidden},
		{"post on a foreign host", "POST", post, "evil.example:4321", true, true, http.StatusForbidden},
		{"wrong token in the link", "GET", "/?t=nope", s.host, false, false, http.StatusForbidden},
		{"the printed link", "GET", "/?t=" + s.token, s.host, false, false, http.StatusSeeOther},
	}
	for _, c := range cases {
		if got := send(c.method, c.target, c.host, c.cookie, c.header); got != c.want {
			t.Errorf("%s: %s %s Host %s: status %d, want %d", c.name, c.method, c.target, c.host, got, c.want)
		}
	}

	// The printed link sets the cookie, strictly same-site.
	r := httptest.NewRequest("GET", "/?t="+s.token, nil)
	r.Host = s.host
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Errorf("framing allowed: %v", w.Header())
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 1 && cookies[0].Name != "lorekeep_token_4321" {
		t.Errorf("cookie %q, want one named for the port", cookies[0].Name)
	}
	if len(cookies) != 1 || cookies[0].Value != s.token || cookies[0].SameSite != http.SameSiteStrictMode || !cookies[0].HttpOnly {
		t.Errorf("cookies = %+v, want one HttpOnly SameSite=Strict token cookie", cookies)
	}
}

// TestStaticEmbedded checks that every file the layout loads is in the exe,
// and that each vendored file is the pinned one, by its recorded SHA-256.
func TestStaticEmbedded(t *testing.T) {
	s, _ := newTestServer(t)
	page := do(t, s, "GET", "/", nil)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /: %d\n%s", page.Code, page.Body)
	}
	// The Content-Security-Policy blocks eval, so htmx must not try it: a
	// trigger filter that fails to evaluate fires on every event.
	if !strings.Contains(page.Body.String(), "&#34;allowEval&#34;: false") && !strings.Contains(page.Body.String(), `"allowEval": false`) {
		t.Error("the layout does not turn off htmx's eval")
	}
	for _, path := range []string{
		"/static/tokens.css", "/static/editor.css", "/static/keys.js", "/static/vendor/htmx.min.js",
	} {
		if !strings.Contains(page.Body.String(), path) {
			t.Errorf("the layout does not load %s", path)
		}
		if w := do(t, s, "GET", path, nil); w.Code != http.StatusOK || w.Body.Len() == 0 {
			t.Errorf("GET %s: %d, %d bytes", path, w.Code, w.Body.Len())
		}
	}
	css, err := fs.ReadFile(staticFiles, "static/tokens.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, font := range []string{
		"JetBrainsMono-Regular.woff2", "JetBrainsMono-Medium.woff2",
		"IBMPlexSans-Regular.woff2", "IBMPlexSans-Medium.woff2",
	} {
		if !bytes.Contains(css, []byte("fonts/"+font)) {
			t.Errorf("tokens.css does not load %s", font)
		}
		if w := do(t, s, "GET", "/static/fonts/"+font, nil); w.Code != http.StatusOK {
			t.Errorf("GET %s: %d", font, w.Code)
		}
	}

	sums, err := fs.ReadFile(staticFiles, "static/SHA256SUMS")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		want, name, ok := strings.Cut(sc.Text(), "  ")
		if !ok {
			t.Fatalf("SHA256SUMS line %q", sc.Text())
		}
		data, err := fs.ReadFile(staticFiles, "static/"+name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got := sha256.Sum256(data); hex.EncodeToString(got[:]) != want {
			t.Errorf("%s does not match its pinned SHA-256", name)
		}
		n++
	}
	if n == 0 {
		t.Error("SHA256SUMS lists nothing")
	}
}

// TestTemplGenerated fails when a .templ file was changed without running
// go tool templ generate, so the committed _templ.go files are what go build
// compiles from the templates.
func TestTemplGenerated(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the templ generator")
	}
	templs, err := filepath.Glob("*.templ")
	if err != nil {
		t.Fatal(err)
	}
	if len(templs) == 0 {
		t.Fatal("no .templ files")
	}
	// The generator's -stdout writes nothing in v0.3, so it runs over copies
	// in a temporary folder; its output does not depend on where it runs.
	tmp := t.TempDir()
	for _, f := range templs {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmp, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("go", "tool", "templ", "generate", "-path", tmp).CombinedOutput(); err != nil {
		t.Fatalf("templ generate: %v: %s", err, out)
	}
	lf := func(b []byte) []byte { return bytes.ReplaceAll(b, []byte{13, 10}, []byte{10}) }
	for _, f := range templs {
		gen := strings.TrimSuffix(f, ".templ") + "_templ.go"
		want, err := os.ReadFile(filepath.Join(tmp, gen))
		if err != nil {
			t.Fatal(err)
		}
		committed, err := os.ReadFile(gen)
		if err != nil {
			t.Fatalf("%s: %v (run go tool templ generate)", gen, err)
		}
		if !bytes.Equal(lf(committed), lf(want)) {
			t.Errorf("%s is out of date: run go tool templ generate -path internal/editor", gen)
		}
	}
}
