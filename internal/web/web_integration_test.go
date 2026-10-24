package web

import (
	"bytes"
	"context"
	"log/slog"
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

func testServer(t *testing.T, loggers ...*slog.Logger) (*Server, *store.Store) {
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
	if _, err := pool.Exec(ctx, `truncate users, groups, group_members, db_connections, db_access, sessions, migration_history, migration_drafts`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	key := make([]byte, 32)
	cfg := config.Config{
		Addr: "127.0.0.1:0", DatabaseURL: dbURL, EncryptionKey: key,
		SessionIdle: 30 * time.Minute, SessionAbsolute: 8 * time.Hour,
		SessionRotate: 15 * time.Minute, SessionGrace: 10 * time.Second,
	}
	sm := auth.NewManager(st, cfg.SessionIdle, cfg.SessionAbsolute, cfg.SessionRotate, cfg.SessionGrace)
	logger := testLogger()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	srv := New(cfg, st, sm, access.New(st), logger)
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

func seedDBConnection(t *testing.T, st *store.Store, name string) domain.DBConnection {
	t.Helper()
	conn, err := st.CreateDBConnection(context.Background(), domain.DBConnection{
		Name: name, Host: "localhost", Port: 5432, DBName: name,
		Username: "u", PasswordEnc: []byte("enc"), SSLMode: "disable",
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	return conn
}

func getWithSession(t *testing.T, srv *Server, path, sid string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: sid})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
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

func TestViewOnlyCannotReachEditStubs(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seedUser(t, st, "budi", "password123", false)
	user, _ := st.GetUserByUsername(ctx, "budi")
	group, err := st.CreateGroup(ctx, "analis", "")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	conn := seedDBConnection(t, st, "demo")
	if err := st.AddGroupMember(ctx, group.ID, user.ID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := st.SetGroupAccess(ctx, group.ID, conn.ID, domain.LevelView); err != nil {
		t.Fatalf("set access: %v", err)
	}

	sid, csrf := login(t, srv, "budi", "password123")
	base := "/databases/" + conn.ID.String()

	for _, path := range []string{base + "/", base + "/schema", base + "/data", base + "/migrations"} {
		rec := getWithSession(t, srv, path, sid)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("GET %s status = %d; want 501 (passed RBAC)", path, rec.Code)
		}
	}

	if rec := getWithSession(t, srv, base+"/migrations/new", sid); rec.Code != http.StatusForbidden {
		t.Fatalf("GET editor status = %d; want 403", rec.Code)
	}
	for _, path := range []string{base + "/migrations", base + "/migrations/explain"} {
		if rec := postForm(t, srv, path, sid, csrf, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("POST %s status = %d; want 403", path, rec.Code)
		}
	}
}

func TestAdminDeniedOnAllDatabaseStubs(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "admin", "password123", true)
	conn := seedDBConnection(t, st, "demo")
	sid, csrf := login(t, srv, "admin", "password123")
	base := "/databases/" + conn.ID.String()
	const wantMessage = "Platform Admin accounts cannot access database tools."

	for _, path := range []string{base + "/", base + "/schema", base + "/data", base + "/migrations", base + "/migrations/new"} {
		rec := getWithSession(t, srv, path, sid)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("GET %s status = %d; want 403", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), wantMessage) {
			t.Fatalf("GET %s body does not contain admin denial message", path)
		}
	}
	for _, path := range []string{base + "/migrations", base + "/migrations/explain"} {
		rec := postForm(t, srv, path, sid, csrf, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("POST %s status = %d; want 403", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), wantMessage) {
			t.Fatalf("POST %s body does not contain admin denial message", path)
		}
	}
}

func TestAdminAlwaysNoneEvenWithLegacyGroupMembership(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	admin := seedUser(t, st, "admin", "password123", true)
	group, err := st.CreateGroup(ctx, "legacy", "")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	conn := seedDBConnection(t, st, "demo")
	if _, err := st.Pool().Exec(ctx, `insert into group_members (group_id, user_id) values ($1,$2)`, group.ID, admin.ID); err != nil {
		t.Fatalf("insert legacy membership: %v", err)
	}
	if err := st.SetGroupAccess(ctx, group.ID, conn.ID, domain.LevelEdit); err != nil {
		t.Fatalf("set access: %v", err)
	}
	if level, err := st.EffectiveLevel(ctx, admin.ID, conn.ID); err != nil || level != domain.LevelEdit {
		t.Fatalf("legacy effective level = %v, %v; want edit", level, err)
	}

	sid, _ := login(t, srv, "admin", "password123")
	rec := getWithSession(t, srv, "/databases/"+conn.ID.String()+"/schema", sid)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("admin legacy access status = %d; want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Platform Admin accounts cannot access database tools.") {
		t.Fatal("admin legacy access should show admin denial message")
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

func TestUserDeniedOnAdminRoute(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "ega", "password123", false)
	sid, csrf := login(t, srv, "ega", "password123")

	if rec := getWithSession(t, srv, "/admin/users", sid); rec.Code != http.StatusForbidden {
		t.Fatalf("GET admin route for non-admin = %d; want 403", rec.Code)
	}
	if rec := postForm(t, srv, "/admin/users", sid, csrf, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("POST admin route for non-admin = %d; want 403", rec.Code)
	}
}

func TestDeniedCrossInterfaceAudited(t *testing.T) {
	var buf bytes.Buffer
	srv, st := testServer(t, testLoggerTo(&buf, slog.LevelInfo))

	admin := seedUser(t, st, "admin", "password123", true)
	user := seedUser(t, st, "budi", "password123", false)
	conn := seedDBConnection(t, st, "demo")

	adminSID, _ := login(t, srv, "admin", "password123")
	if rec := getWithSession(t, srv, "/databases/"+conn.ID.String()+"/schema", adminSID); rec.Code != http.StatusForbidden {
		t.Fatalf("admin db route status = %d; want 403", rec.Code)
	}
	userSID, _ := login(t, srv, "budi", "password123")
	if rec := getWithSession(t, srv, "/admin/users", userSID); rec.Code != http.StatusForbidden {
		t.Fatalf("user admin route status = %d; want 403", rec.Code)
	}

	logs := buf.String()
	for _, want := range []string{
		"actor_id=" + admin.ID.String(),
		"target_id=" + conn.ID.String(),
		"action=view_schema",
		"action=admin",
		"result=forbidden",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("audit log does not contain %q:\n%s", want, logs)
		}
	}
	if !strings.Contains(logs, "actor_id="+user.ID.String()) {
		t.Fatalf("audit log does not contain user actor id:\n%s", logs)
	}
	for _, secret := range []string{"password123", "postgres://", "csrf_token", "sid="} {
		if strings.Contains(logs, secret) {
			t.Fatalf("audit log leaked %q:\n%s", secret, logs)
		}
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
