package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"leafygo/internal/config"
	"leafygo/internal/telemetry"
)

func spaTestServer(t *testing.T) *Server {
	t.Helper()
	return &Server{
		cfg:    config.Config{},
		logger: telemetry.NewLogger(telemetry.LoggerOptions{Writer: &strings.Builder{}}),
	}
}

func TestSPAServesIndexAndHistoryFallback(t *testing.T) {
	s := spaTestServer(t)
	handler := s.Handler()

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "root redirect", path: "/app", wantStatus: http.StatusPermanentRedirect},
		{name: "index", path: "/app/", wantStatus: http.StatusOK, wantBody: `id="root"`},
		{name: "nested route fallback", path: "/app/databases/db-1/overview", wantStatus: http.StatusOK, wantBody: `id="root"`},
		{name: "missing asset", path: "/app/assets/does-not-exist.js", wantStatus: http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantBody != "" && !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("body missing %q: %s", tc.wantBody, rec.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-cache") {
					t.Fatalf("index Cache-Control = %q", got)
				}
			}
		})
	}
}

func TestSPAServesHashedAssetsWithImmutableCache(t *testing.T) {
	entries, err := fs.ReadDir(spaAssets, "dist/assets")
	if err != nil {
		t.Fatalf("read embedded assets: %v", err)
	}
	var jsName string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".js") {
			jsName = entry.Name()
			break
		}
	}
	if jsName == "" {
		t.Fatal("no embedded JS asset found; run npm run build in web/")
	}

	s := spaTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/assets/"+jsName, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Fatalf("asset Cache-Control = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
		t.Fatalf("Content-Type = %q", got)
	}
}

func TestSPACSPAllowsInlineStylesButNotInlineScripts(t *testing.T) {
	s := spaTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "style-src 'self' 'unsafe-inline'") {
		t.Fatalf("SPA CSP must allow inline styles for CodeMirror: %q", csp)
	}
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("SPA CSP must not allow inline scripts: %q", csp)
	}
}

func TestSPARootRedirectsDashboard(t *testing.T) {
	s := spaTestServer(t)
	s.cfg.SPARoot = true
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusPermanentRedirect {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("Location") != "/app/" {
		t.Fatalf("Location = %q", rec.Header().Get("Location"))
	}
}

func TestM1TemplateLoginStillServed(t *testing.T) {
	s := spaTestServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Sign in to") {
		t.Fatalf("M1 login template changed unexpectedly")
	}
}
