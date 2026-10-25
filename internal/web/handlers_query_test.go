package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"leafygo/internal/domain"
	"leafygo/internal/store"
)

type queryTestEnv struct {
	srv      *Server
	st       *store.Store
	conn     domain.DBConnection
	viewUser domain.User
	viewSID  string
	viewCSRF string
	editUser domain.User
	editSID  string
	editCSRF string
	adminSID string
	base     string
}

func newQueryTestEnv(t *testing.T) queryTestEnv {
	t.Helper()
	tc := targetConfig(t)
	srv, st := testServer(t)
	ctx := context.Background()

	admin := seedUser(t, st, "admin", "password123", true)
	viewUser := seedUser(t, st, "viewer_user", "password123", false)
	editUser := seedUser(t, st, "editor_user", "password123", false)

	adminSID, adminCSRF := login(t, srv, admin.Username, "password123")
	rec := postForm(t, srv, "/admin/databases", adminSID, adminCSRF, url.Values{
		"name": {"TargetDB"}, "host": {tc.Host}, "port": {strconv.Itoa(int(tc.Port))}, "dbname": {tc.Database},
		"username": {tc.User}, "password": {tc.Password}, "sslmode": {"disable"},
	})
	assertStatus(t, rec, http.StatusSeeOther, "create connection")

	conns, err := st.ListDBConnections(ctx)
	if err != nil || len(conns) == 0 {
		t.Fatalf("list connections: %v", err)
	}
	dbConn := conns[0]

	viewGroup, _ := st.CreateGroup(ctx, "viewers", "")
	_ = st.AddGroupMember(ctx, viewGroup.ID, viewUser.ID)
	_ = st.SetGroupAccess(ctx, viewGroup.ID, dbConn.ID, domain.LevelView)

	editGroup, _ := st.CreateGroup(ctx, "editors", "")
	_ = st.AddGroupMember(ctx, editGroup.ID, editUser.ID)
	_ = st.SetGroupAccess(ctx, editGroup.ID, dbConn.ID, domain.LevelEdit)

	viewSID, viewCSRF := login(t, srv, viewUser.Username, "password123")
	editSID, editCSRF := login(t, srv, editUser.Username, "password123")

	return queryTestEnv{
		srv:      srv,
		st:       st,
		conn:     dbConn,
		viewUser: viewUser,
		viewSID:  viewSID,
		viewCSRF: viewCSRF,
		editUser: editUser,
		editSID:  editSID,
		editCSRF: editCSRF,
		adminSID: adminSID,
		base:     "/databases/" + dbConn.ID.String(),
	}
}

func TestQueryConsoleAccessAndPermissions(t *testing.T) {
	e := newQueryTestEnv(t)

	// 1. GET /query: View and Edit allowed; Admin 403
	rec := getWithSession(t, e.srv, e.base+"/query", e.viewSID)
	assertStatus(t, rec, http.StatusOK, "view user GET /query")
	assertBody(t, rec, "SQL Console", "contains SQL Console header/tab")
	assertBody(t, rec, "Read only", "contains Read only badge")

	rec = getWithSession(t, e.srv, e.base+"/query", e.editSID)
	assertStatus(t, rec, http.StatusOK, "edit user GET /query")

	rec = getWithSession(t, e.srv, e.base+"/query", e.adminSID)
	assertStatus(t, rec, http.StatusForbidden, "admin user GET /query")

	// 2. POST /query without CSRF rejected with 403
	noCSRF := postForm(t, e.srv, e.base+"/query", e.viewSID, "wrong_token", url.Values{"sql": {"SELECT 1"}})
	assertStatus(t, noCSRF, http.StatusForbidden, "invalid CSRF POST /query")

	// 3. POST /query/to-draft: Edit allowed; View 403; Admin 403
	viewDraft := postForm(t, e.srv, e.base+"/query/to-draft", e.viewSID, e.viewCSRF, url.Values{"sql": {"INSERT INTO t VALUES (1)"}})
	assertStatus(t, viewDraft, http.StatusForbidden, "view user POST /query/to-draft")

	adminDraft := postForm(t, e.srv, e.base+"/query/to-draft", e.adminSID, "any", url.Values{"sql": {"INSERT INTO t VALUES (1)"}})
	assertStatus(t, adminDraft, http.StatusForbidden, "admin user POST /query/to-draft")
}

func TestQueryConsoleExecutionFlow(t *testing.T) {
	e := newQueryTestEnv(t)

	// 1. SELECT query execution succeeds with HTTP 200
	rec := postForm(t, e.srv, e.base+"/query", e.viewSID, e.viewCSRF, url.Values{"sql": {"SELECT 42 AS my_num, 'hello' AS my_str"}})
	assertStatus(t, rec, http.StatusOK, "execute SELECT query")
	assertBody(t, rec, "my_num", "table header contains column")
	assertBody(t, rec, "my_str", "table header contains column")
	assertBody(t, rec, "42", "table contains row value")
	assertBody(t, rec, "hello", "table contains row value")
	assertBody(t, rec, "1 row", "row count label")

	// 2. Write rejection: allowlist keyword rejection (INSERT) -> 422
	// Edit user sees the "Buat draft migration dari SQL ini" button inside a single error alert (no warning alert)
	editRec := postForm(t, e.srv, e.base+"/query", e.editSID, e.editCSRF, url.Values{"sql": {"INSERT INTO users (name) VALUES ('alice')"}})
	assertStatus(t, editRec, http.StatusUnprocessableEntity, "write statement rejected")
	assertBody(t, editRec, "alert alert--error", "write rejection uses error alert")
	if strings.Contains(editRec.Body.String(), "alert--warning") {
		t.Fatal("expected no alert--warning on write rejection")
	}
	assertBody(t, editRec, "Console hanya untuk membaca data", "write rejection message")
	assertBody(t, editRec, "/migrations", "write rejection contains migration link")
	assertBody(t, editRec, "Buat draft migration dari SQL ini", "edit user sees to-draft button")
	assertBody(t, editRec, "INSERT INTO users (name) VALUES (&#39;alice&#39;)", "SQL preserved in editor")

	// View user does NOT see "Buat draft migration dari SQL ini" button and also gets no warning alert
	viewRec := postForm(t, e.srv, e.base+"/query", e.viewSID, e.viewCSRF, url.Values{"sql": {"INSERT INTO users (name) VALUES ('alice')"}})
	assertStatus(t, viewRec, http.StatusUnprocessableEntity, "write statement rejected for view user")
	assertBody(t, viewRec, "alert alert--error", "view user write rejection uses error alert")
	if strings.Contains(viewRec.Body.String(), "alert--warning") {
		t.Fatal("expected no alert--warning on view user write rejection")
	}
	assertBody(t, viewRec, "Console hanya untuk membaca data", "write rejection message")
	if strings.Contains(viewRec.Body.String(), "Buat draft migration dari SQL ini") {
		t.Fatal("View user should NOT see 'Buat draft migration dari SQL ini' button")
	}

	// 3. Multi-statement rejected -> 422
	multiRec := postForm(t, e.srv, e.base+"/query", e.viewSID, e.viewCSRF, url.Values{"sql": {"SELECT 1; SELECT 2"}})
	assertStatus(t, multiRec, http.StatusUnprocessableEntity, "multi-statement rejected")
	assertBody(t, multiRec, "only runs one statement at a time", "multi-statement error")

	// 4. Target SQL syntax error -> 422 with SQLSTATE
	syntaxRec := postForm(t, e.srv, e.base+"/query", e.viewSID, e.viewCSRF, url.Values{"sql": {"SELECT FROM"}})
	assertStatus(t, syntaxRec, http.StatusUnprocessableEntity, "syntax error")
	assertBody(t, syntaxRec, "42601", "SQLSTATE 42601 present")
}

func TestQueryToDraftHandoff(t *testing.T) {
	e := newQueryTestEnv(t)
	ctx := context.Background()

	sqlText := "CREATE TABLE console_handoff (id int primary key);"
	rec := postForm(t, e.srv, e.base+"/query/to-draft", e.editSID, e.editCSRF, url.Values{
		"sql": {sqlText},
	})
	assertStatus(t, rec, http.StatusSeeOther, "to-draft redirect")
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "/migrations/drafts/") {
		t.Fatalf("expected redirect to draft editor, got %q", loc)
	}

	// Verify draft created in store
	drafts, err := e.st.ListMigrationDrafts(ctx, e.conn.ID)
	if err != nil || len(drafts) == 0 {
		t.Fatalf("draft not found in store: %v", err)
	}
	latest, err := e.st.GetMigrationDraft(ctx, e.conn.ID, drafts[0].ID)
	if err != nil {
		t.Fatalf("get draft: %v", err)
	}
	if latest.SQLText != sqlText {
		t.Fatalf("expected SQL %q, got %q", sqlText, latest.SQLText)
	}
	if latest.Title != "Dari SQL Console" {
		t.Fatalf("expected default title 'Dari SQL Console', got %q", latest.Title)
	}

	// Verify migration history was NOT created
	hist, err := e.st.ListMigrationHistory(ctx, e.conn.ID, 10)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if len(hist) != 0 {
		t.Fatalf("expected 0 migration history records, got %d", len(hist))
	}
}

func TestQueryBodyLimitEnforced(t *testing.T) {
	e := newQueryTestEnv(t)

	// Maximum query body limit is 4 * MAX_QUERY_BYTES + 64 KiB
	// Default MAX_QUERY_BYTES is 65536, so limit is 327680 bytes.
	// Creating body larger than 327680 bytes should return 413.
	oversizedSQL := "SELECT '" + strings.Repeat("a", 350000) + "'"
	form := url.Values{"sql": {oversizedSQL}, "csrf_token": {e.viewCSRF}}
	body := form.Encode()

	req := httptest.NewRequest(http.MethodPost, e.base+"/query", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "sid", Value: e.viewSID})
	rec := httptest.NewRecorder()

	e.srv.Handler().ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusRequestEntityTooLarge, "oversized body returns 413")
}

func TestQueryConsoleTargetUnreachable(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	user := seedUser(t, st, "budi", "password123", false)
	conn := seedDBConnection(t, st, "unreachable_db")
	group, _ := st.CreateGroup(ctx, "viewers", "")
	_ = st.AddGroupMember(ctx, group.ID, user.ID)
	_ = st.SetGroupAccess(ctx, group.ID, conn.ID, domain.LevelView)

	sid, csrf := login(t, srv, "budi", "password123")
	base := "/databases/" + conn.ID.String()

	// 1. GET /query on unreachable DB returns 200 with targetError banner and no query editor/form
	rec := getWithSession(t, srv, base+"/query", sid)
	assertStatus(t, rec, http.StatusOK, "unreachable target GET /query")
	assertBody(t, rec, "Cannot connect to the database", "shows target error alert")
	assertBody(t, rec, "Schema, data, and SQL console are unavailable for now.", "shows updated unavailable notice")

	body := rec.Body.String()
	// Verify query form and textarea are NOT rendered
	if strings.Contains(body, "<form class=\"form\" method=\"post\" action=\"/databases/"+conn.ID.String()+"/query\">") {
		t.Fatal("query form should not be rendered when target is unreachable")
	}
	if strings.Contains(body, "id=\"query-sql\"") {
		t.Fatal("query-sql textarea should not be rendered when target is unreachable")
	}

	// Verify tabs navigation does NOT contain Schema, Data, or SQL Console
	if strings.Contains(body, base+"/schema") {
		t.Fatal("navigation tabs should not contain Schema link when target is unreachable")
	}
	if strings.Contains(body, base+"/data") {
		t.Fatal("navigation tabs should not contain Data link when target is unreachable")
	}
	if strings.Contains(body, base+"/query") {
		t.Fatal("navigation tabs should not contain SQL Console link when target is unreachable")
	}

	// 2. POST /query on unreachable DB returns 200 with targetError banner, does not run query
	postRec := postForm(t, srv, base+"/query", sid, csrf, url.Values{"sql": {"SELECT 1"}})
	assertStatus(t, postRec, http.StatusOK, "unreachable target POST /query")
	assertBody(t, postRec, "Cannot connect to the database", "shows target error alert on POST")
	if strings.Contains(postRec.Body.String(), "id=\"query-sql\"") {
		t.Fatal("query-sql textarea should not be rendered on POST when target is unreachable")
	}
}

