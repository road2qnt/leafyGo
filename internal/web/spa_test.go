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

func TestSPARootRedirectsAllLegacyM1Routes(t *testing.T) {
	s := spaTestServer(t)
	s.cfg.SPARoot = true
	handler := s.Handler()

	cases := []struct {
		path     string
		wantLoc  string
		wantCode int
	}{
		{path: "/", wantLoc: "/app/", wantCode: http.StatusPermanentRedirect},
		{path: "/login", wantLoc: "/app/login", wantCode: http.StatusPermanentRedirect},
		{path: "/admin/users", wantLoc: "/app/admin/users", wantCode: http.StatusPermanentRedirect},
		{path: "/admin/groups", wantLoc: "/app/admin/groups", wantCode: http.StatusPermanentRedirect},
		{path: "/admin/databases", wantLoc: "/app/admin/databases", wantCode: http.StatusPermanentRedirect},
		{path: "/databases/db-1/schema?tab=cols", wantLoc: "/app/databases/db-1/schema?tab=cols", wantCode: http.StatusPermanentRedirect},
		{path: "/unknown-legacy-page", wantLoc: "/app/", wantCode: http.StatusFound},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.wantCode {
				t.Fatalf("GET %s status = %d, want %d", tc.path, rec.Code, tc.wantCode)
			}
			if got := rec.Header().Get("Location"); got != tc.wantLoc {
				t.Fatalf("GET %s Location = %q, want %q", tc.path, got, tc.wantLoc)
			}
		})
	}

	// Legacy M1 form POSTs must not be handled when SPA_ROOT=true.
	for _, path := range []string{"/login", "/logout", "/admin/users"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
			t.Fatalf("POST %s status = %d, want 404 or 405", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "<html") {
			t.Fatalf("POST %s rendered HTML template unexpectedly", path)
		}
	}
}
