package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"leafygo/internal/access"
	"leafygo/internal/auth"
	"leafygo/internal/config"
	"leafygo/internal/domain"
	"leafygo/internal/platform"
	"leafygo/internal/store"
)

func testServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL is empty; skipping integration test")
	}
	ctx := context.Background()
	if err := platform.Migrate(ctx, dbURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	st := store.New(pool)
	if _, err := pool.Exec(ctx, `truncate users, groups, group_members, db_connections, db_access, sessions`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	key := make([]byte, 32)
	cfg := config.Config{
		Addr: "127.0.0.1:0", DatabaseURL: dbURL, EncryptionKey: key,
		SessionIdle: 30 * time.Minute, SessionAbsolute: 8 * time.Hour,
		SessionRotate: 15 * time.Minute, SessionGrace: 10 * time.Second,
	}
	sm := auth.NewManager(st, cfg.SessionIdle, cfg.SessionAbsolute, cfg.SessionRotate, cfg.SessionGrace)
	srv := New(cfg, st, sm, access.New(st), testLogger())
	return srv, st
}

func seedUser(t *testing.T, st *store.Store, username, password string, admin bool) domain.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := st.CreateUser(context.Background(), username, hash, admin)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func login(t *testing.T, srv *Server, username, password string) (sid, csrf string) {
	t.Helper()
	form := url.Values{"username": {username}, "password": {password}}

	get := httptest.NewRequest(http.MethodGet, "/login", nil)
	getRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRec, get)
	var loginCSRF string
	for _, c := range getRec.Result().Cookies() {
		if c.Name == "csrf_login" {
			loginCSRF = c.Value
		}
	}
	if loginCSRF == "" {
		t.Fatal("no csrf_login cookie")
	}
	form.Set("csrf_token", loginCSRF)

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_login", Value: loginCSRF})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "sid" {
			sid = c.Value
		}
	}
	if sid == "" {
		t.Fatal("no sid cookie after login")
	}
	sess, err := srv.store.GetSession(context.Background(), auth.HashToken(sid))
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	return sid, sess.CSRFToken
}

func postForm(t *testing.T, srv *Server, path, sid, csrf string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set("csrf_token", csrf)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "sid", Value: sid})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestLoginFlowAndCookieFlags(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "ega", "password123", false)

	sid, _ := login(t, srv, "ega", "password123")
	found := false
	form := url.Values{"username": {"ega"}, "password": {"password123"}}
	get := httptest.NewRequest(http.MethodGet, "/login", nil)
	getRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRec, get)
	var loginCSRF string
	for _, c := range getRec.Result().Cookies() {
		if c.Name == "csrf_login" {
			loginCSRF = c.Value
		}
	}
	form.Set("csrf_token", loginCSRF)
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_login", Value: loginCSRF})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == "sid" {
			found = true
			if !c.HttpOnly {
				t.Error("sid cookie must be HttpOnly")
			}
			if c.SameSite != http.SameSiteLaxMode {
				t.Error("sid cookie must be SameSite=Lax")
			}
		}
	}
	if !found {
		t.Fatal("no sid cookie")
	}

	dash := httptest.NewRequest(http.MethodGet, "/", nil)
	dash.AddCookie(&http.Cookie{Name: "sid", Value: sid})
	dashRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(dashRec, dash)
	if dashRec.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d", dashRec.Code)
	}
}

func TestRBACViewOnlyCannotReachEditRoute(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seedUser(t, st, "budi", "password123", false)
	user, _ := st.GetUserByUsername(ctx, "budi")
	group, err := st.CreateGroup(ctx, "analis", "")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	conn, err := st.CreateDBConnection(ctx, domain.DBConnection{
		Name: "demo", Host: "localhost", Port: 5432, DBName: "demo",
		Username: "u", PasswordEnc: []byte("enc"), SSLMode: "disable",
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if err := st.AddGroupMember(ctx, group.ID, user.ID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := st.SetGroupAccess(ctx, group.ID, conn.ID, domain.LevelView); err != nil {
		t.Fatalf("set access: %v", err)
	}

	sid, csrf := login(t, srv, "budi", "password123")

	viewReq := httptest.NewRequest(http.MethodGet, "/databases/"+conn.ID.String()+"/schema", nil)
	viewReq.AddCookie(&http.Cookie{Name: "sid", Value: sid})
	viewRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(viewRec, viewReq)
	if viewRec.Code != http.StatusNotImplemented {
		t.Fatalf("view route status = %d; want 501 (passed RBAC)", viewRec.Code)
	}

	rec := postForm(t, srv, "/databases/"+conn.ID.String()+"/migrations", sid, csrf, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("edit route status = %d; want 403", rec.Code)
	}
}

func TestCSRFRejected(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "ega", "password123", false)
	sid, _ := login(t, srv, "ega", "password123")

	req := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader("csrf_token=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "sid", Value: sid})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("logout with bad CSRF = %d; want 403", rec.Code)
	}
}

func TestAdminRouteRequiresPlatformAdmin(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "ega", "password123", false)
	sid, _ := login(t, srv, "ega", "password123")

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: sid})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("admin route for non-admin = %d; want 403", rec.Code)
	}
}

func TestDashboardOnlyShowsAccessibleDBs(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seedUser(t, st, "ega", "password123", false)
	seedUser(t, st, "outsider", "password123", false)
	user, _ := st.GetUserByUsername(ctx, "ega")
	group, _ := st.CreateGroup(ctx, "eng", "")
	conn, err := st.CreateDBConnection(ctx, domain.DBConnection{
		Name: "demo", Host: "localhost", Port: 5432, DBName: "demo",
		Username: "u", PasswordEnc: []byte("enc"), SSLMode: "disable",
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if err := st.AddGroupMember(ctx, group.ID, user.ID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := st.SetGroupAccess(ctx, group.ID, conn.ID, domain.LevelView); err != nil {
		t.Fatalf("set access: %v", err)
	}

	sid, _ := login(t, srv, "ega", "password123")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: sid})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "demo") {
		t.Fatal("dashboard should list accessible db")
	}

	sid2, _ := login(t, srv, "outsider", "password123")
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(&http.Cookie{Name: "sid", Value: sid2})
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	if strings.Contains(rec2.Body.String(), "demo") {
		t.Fatal("dashboard must not list db the user cannot access")
	}
}
