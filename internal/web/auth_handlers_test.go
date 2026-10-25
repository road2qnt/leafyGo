package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"leafygo/internal/auth"
)

func TestSafeNext(t *testing.T) {
	tests := map[string]string{
		"":                    "/",
		"/databases/abc":      "/databases/abc",
		"/admin/users?edit=1": "/admin/users?edit=1",
		"https://evil.test/":  "/",
		"//evil.test/x":       "/",
		"/\\evil.test":        "/",
		"/ok\r\nSet-Cookie:x": "/",
		"/login":              "/",
		"/login?next=/":       "/",
		"relative/path":       "/",
		"  /padded  ":         "/padded",
	}
	for in, want := range tests {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestLoginLimiter(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := newLoginLimiter(3, time.Minute)
	l.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if !l.allowed("k") {
			t.Fatalf("attempt %d blocked before reaching the limit", i)
		}
		l.fail("k")
	}
	if l.allowed("k") {
		t.Fatal("key should be blocked after max failures")
	}
	if !l.allowed("other") {
		t.Fatal("limit must be per key")
	}

	now = now.Add(2 * time.Minute)
	if !l.allowed("k") {
		t.Fatal("failures should expire after the window")
	}

	l.fail("k")
	l.reset("k")
	if len(l.failures["k"]) != 0 {
		t.Fatal("reset should clear failures")
	}
}

func attemptLogin(srv *Server, username, password string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	get := httptest.NewRecorder()
	srv.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/login", nil))
	var csrf string
	for _, c := range get.Result().Cookies() {
		if c.Name == "csrf_login" {
			csrf = c.Value
		}
	}
	form := url.Values{"username": {username}, "password": {password}, "csrf_token": {csrf}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_login", Value: csrf})
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func responseCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "ega", "password123", false)
	inactive := seedUser(t, st, "eko", "password123", false)
	admin := seedUser(t, st, "admin", "password123", true)
	if err := st.SetUserActiveGuarded(context.Background(), inactive.ID, false, admin.ID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	tests := []struct{ name, user, pass string }{
		{"wrong password", "ega", "nope-nope-nope"},
		{"unknown user", "ghost", "password123"},
		{"empty password", "ega", ""},
		{"inactive account", "eko", "password123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := attemptLogin(srv, tt.user, tt.pass)
			assertStatus(t, rec, http.StatusSeeOther, "login")
			if loc := rec.Header().Get("Location"); loc != "/login" {
				t.Fatalf("redirect = %q; want /login", loc)
			}
			if c := responseCookie(rec, "sid"); c != nil && c.Value != "" {
				t.Fatal("failed login must not issue a session")
			}
		})
	}
}

func TestLoginErrorDoesNotRevealWhichFieldWasWrong(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "ega", "password123", false)

	flash := func(rec *httptest.ResponseRecorder) string {
		c := responseCookie(rec, "flash")
		if c == nil {
			t.Fatal("no flash cookie")
		}
		return c.Value
	}
	wrongPass := flash(attemptLogin(srv, "ega", "wrong-password"))
	unknown := flash(attemptLogin(srv, "ghost", "wrong-password"))
	if wrongPass != unknown {
		t.Fatalf("flash differs between wrong password and unknown user:\n%q\n%q", wrongPass, unknown)
	}
}

func TestLoginRateLimitBlocksCorrectPassword(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "ega", "password123", false)
	for i := 0; i < 8; i++ {
		attemptLogin(srv, "ega", "wrong-password")
	}
	rec := attemptLogin(srv, "ega", "password123")
	assertStatus(t, rec, http.StatusSeeOther, "throttled login")
	if c := responseCookie(rec, "sid"); c != nil && c.Value != "" {
		t.Fatal("throttled login must not issue a session even with the right password")
	}
}

func TestLoginRequiresLoginCSRF(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "ega", "password123", false)

	form := url.Values{"username": {"ega"}, "password": {"password123"}, "csrf_token": {"forged"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusForbidden, "login without csrf cookie")

	req = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_login", Value: "different"})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusForbidden, "login with mismatched csrf")
}

func TestLoginRevokesPreviousSession(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "ega", "password123", false)
	oldSID, _ := login(t, srv, "ega", "password123")

	rec := attemptLogin(srv, "ega", "password123", &http.Cookie{Name: "sid", Value: oldSID})
	assertStatus(t, rec, http.StatusSeeOther, "re-login")
	newSID := responseCookie(rec, "sid")
	if newSID == nil || newSID.Value == "" || newSID.Value == oldSID {
		t.Fatalf("login must issue a fresh identifier, got %v", newSID)
	}
	if rec := getWithSession(t, srv, "/", oldSID); rec.Code != http.StatusSeeOther {
		t.Fatalf("pre-login session still valid: status %d", rec.Code)
	}
}

func TestLoginHonoursSafeNextOnly(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "ega", "password123", false)
	_ = st

	for next, want := range map[string]string{"/databases": "/databases", "https://evil.test": "/", "//evil.test": "/"} {
		get := httptest.NewRecorder()
		srv.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/login", nil))
		csrf := responseCookie(get, "csrf_login").Value
		form := url.Values{"username": {"ega"}, "password": {"password123"}, "csrf_token": {csrf}, "next": {next}}
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "csrf_login", Value: csrf})
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if loc := rec.Header().Get("Location"); loc != want {
			t.Errorf("next=%q redirected to %q; want %q", next, loc, want)
		}
	}
}

func TestLogoutRevokesSessionOnBackend(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "ega", "password123", false)
	sid, csrf := login(t, srv, "ega", "password123")

	rec := postForm(t, srv, "/logout", sid, csrf, nil)
	assertStatus(t, rec, http.StatusSeeOther, "logout")
	if c := responseCookie(rec, "sid"); c == nil || c.Value != "" || c.MaxAge >= 0 {
		t.Fatalf("logout must expire the cookie, got %+v", c)
	}
	sess, err := st.GetSession(context.Background(), auth.HashToken(sid))
	if err != nil || sess.RevokedAt == nil {
		t.Fatalf("session must be revoked in the backend: %+v, %v", sess, err)
	}
	rec = getWithSession(t, srv, "/", sid)
	assertStatus(t, rec, http.StatusSeeOther, "reuse after logout")
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Fatalf("redirect = %q; want /login", loc)
	}
}

func TestLogoutWithBadCSRFKeepsSession(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "ega", "password123", false)
	sid, _ := login(t, srv, "ega", "password123")

	assertStatus(t, postForm(t, srv, "/logout", sid, "forged", nil), http.StatusForbidden, "forged logout")
	assertStatus(t, getWithSession(t, srv, "/", sid), http.StatusOK, "session after forged logout")
}

func TestProtectedRoutesRequireSession(t *testing.T) {
	srv, _ := testServer(t)
	for _, path := range []string{"/", "/admin/users", "/admin/groups", "/admin/databases", "/databases/00000000-0000-0000-0000-000000000000/schema"} {
		for _, sid := range []string{"", "not-a-real-session"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			if sid != "" {
				req.AddCookie(&http.Cookie{Name: "sid", Value: sid})
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
				t.Errorf("GET %s with sid=%q: status %d location %q; want redirect to /login", path, sid, rec.Code, rec.Header().Get("Location"))
			}
		}
	}
	for _, path := range []string{"/logout", "/admin/users", "/admin/groups", "/admin/databases"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(""))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("anonymous POST %s status = %d; want redirect", path, rec.Code)
		}
	}
}

func TestSessionCookieSecureInProduction(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "ega", "password123", false)

	for _, secure := range []bool{false, true} {
		srv.cfg.CookieSecure = secure
		rec := attemptLogin(srv, "ega", "password123")
		c := responseCookie(rec, "sid")
		if c == nil {
			t.Fatal("no sid cookie")
		}
		if c.Secure != secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
			t.Errorf("CookieSecure=%v produced cookie %+v", secure, c)
		}
		if csrf := responseCookie(attemptLogin(srv, "ega", "wrong"), "flash"); csrf == nil || csrf.Secure != secure {
			t.Errorf("flash cookie Secure flag must follow CookieSecure=%v", secure)
		}
	}
}

func TestSessionIdentifierIsOpaqueAndStoredHashed(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "ega", "password123", false)
	first, _ := login(t, srv, "ega", "password123")
	second, _ := login(t, srv, "ega", "password123")

	if len(first) < 32 || first == second {
		t.Fatalf("identifiers must be long and unique: %q, %q", first, second)
	}
	if strings.Contains(first, "ega") {
		t.Fatal("identifier must not embed user data")
	}
	var n int
	if err := st.Pool().QueryRow(context.Background(), `select count(*) from sessions where encode(id_hash,'escape') = $1`, first).Scan(&n); err == nil && n != 0 {
		t.Fatal("raw identifier must not be stored in the backend")
	}
	if _, err := st.GetSession(context.Background(), []byte(first)); err == nil {
		t.Fatal("lookup by raw identifier must fail, only the hash is stored")
	}
}

func TestSecretsNeverAppearInURLsOrResponses(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "admin", "password123", true)
	sid, csrf := login(t, srv, "admin", "password123")

	checks := []*httptest.ResponseRecorder{
		attemptLogin(srv, "admin", "password123"),
		attemptLogin(srv, "admin", "password123"),
		getWithSession(t, srv, "/admin/users", sid),
		postForm(t, srv, "/admin/users", sid, csrf, url.Values{"username": {"budi"}, "password": {"password123"}}),
		getWithSession(t, srv, "/", sid),
	}
	for i, rec := range checks {
		body := rec.Body.String()
		if strings.Contains(body, "password123") {
			t.Errorf("response %d echoes the password", i)
		}
		if strings.Contains(body, "argon2") || strings.Contains(body, "$2a$") {
			t.Errorf("response %d exposes a password hash", i)
		}
		if loc := rec.Header().Get("Location"); strings.Contains(loc, "password123") || strings.Contains(loc, sid) {
			t.Errorf("response %d puts a secret in the redirect URL %q", i, loc)
		}
	}
	if body := getWithSession(t, srv, "/admin/users", sid).Body.String(); strings.Contains(body, sid) {
		t.Error("the session identifier must not be rendered into pages")
	}
}
