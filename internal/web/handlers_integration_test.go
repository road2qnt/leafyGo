package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"leafygo/internal/domain"
)

const testSchema = "lg_web_test"

func targetConfig(t *testing.T) *pgx.ConnConfig {
	t.Helper()
	raw := os.Getenv("TEST_TARGET_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_TARGET_DATABASE_URL not set")
	}
	cfg, err := pgx.ParseConfig(raw)
	if err != nil {
		t.Fatalf("parse target url: %v", err)
	}
	conn, err := pgx.ConnectConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect target: %v", err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), `
		drop schema if exists `+testSchema+` cascade;
		create schema `+testSchema+`;
		create table `+testSchema+`.items (id int primary key, name text not null, note text);
		insert into `+testSchema+`.items select g, 'item ' || g, case when g % 2 = 0 then null else 'odd' end
		from generate_series(1, 7) g;`); err != nil {
		t.Fatalf("prepare target: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.ConnectConfig(context.Background(), cfg)
		if err == nil {
			_, _ = c.Exec(context.Background(), `drop schema if exists `+testSchema+` cascade`)
			c.Close(context.Background())
		}
	})
	return cfg
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int, what string) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("%s status = %d; want %d\n%s", what, rec.Code, want, rec.Body.String())
	}
}

func assertBody(t *testing.T, rec *httptest.ResponseRecorder, want, what string) {
	t.Helper()
	if !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("%s body does not contain %q", what, want)
	}
}

func TestAdminSetupThenViewerAndMigration(t *testing.T) {
	tc := targetConfig(t)
	srv, st := testServer(t)
	ctx := context.Background()
	seedUser(t, st, "admin", "password123", true)
	adminSID, adminCSRF := login(t, srv, "admin", "password123")

	rec := postForm(t, srv, "/admin/users", adminSID, adminCSRF, url.Values{"username": {"budi"}, "password": {"password123"}})
	assertStatus(t, rec, http.StatusSeeOther, "create user")
	rec = postForm(t, srv, "/admin/users", adminSID, adminCSRF, url.Values{"username": {"bad name"}, "password": {"short"}})
	assertStatus(t, rec, http.StatusUnprocessableEntity, "invalid user")

	rec = postForm(t, srv, "/admin/groups", adminSID, adminCSRF, url.Values{"name": {"backend"}})
	assertStatus(t, rec, http.StatusSeeOther, "create group")
	group, err := st.GetGroupByName(ctx, "backend")
	if err != nil {
		t.Fatalf("group: %v", err)
	}

	rec = postForm(t, srv, "/admin/databases", adminSID, adminCSRF, url.Values{
		"name": {"Target"}, "host": {tc.Host}, "port": {strconv.Itoa(int(tc.Port))}, "dbname": {tc.Database},
		"username": {tc.User}, "password": {tc.Password}, "sslmode": {"disable"},
	})
	assertStatus(t, rec, http.StatusSeeOther, "create connection")
	conns, err := st.ListDBConnections(ctx)
	if err != nil || len(conns) != 1 {
		t.Fatalf("connections = %v, %v", conns, err)
	}
	conn := conns[0]

	rec = postForm(t, srv, "/admin/databases/"+conn.ID.String()+"/test", adminSID, adminCSRF, nil)
	assertStatus(t, rec, http.StatusSeeOther, "test connection")

	budi, _ := st.GetUserByUsername(ctx, "budi")
	groupBase := "/admin/groups/" + group.ID.String()
	assertStatus(t, postForm(t, srv, groupBase+"/members", adminSID, adminCSRF, url.Values{"action": {"add"}, "user_id": {budi.ID.String()}}), http.StatusSeeOther, "add member")
	assertStatus(t, postForm(t, srv, groupBase+"/access", adminSID, adminCSRF, url.Values{"db_id": {conn.ID.String()}, "level": {"edit"}}), http.StatusSeeOther, "grant access")
	if level, _ := st.EffectiveLevel(ctx, budi.ID, conn.ID); level != domain.LevelEdit {
		t.Fatalf("effective level = %v; want edit", level)
	}

	for _, path := range []string{"/admin/users", "/admin/users?edit=" + budi.ID.String(), "/admin/groups", groupBase, "/admin/databases", "/admin/databases/" + conn.ID.String()} {
		assertStatus(t, getWithSession(t, srv, path, adminSID), http.StatusOK, "GET "+path)
	}

	sid, csrf := login(t, srv, "budi", "password123")
	base := "/databases/" + conn.ID.String()

	rec = getWithSession(t, srv, base+"/", sid)
	assertStatus(t, rec, http.StatusOK, "overview")
	assertBody(t, rec, "Connected", "overview")

	rec = getWithSession(t, srv, base+"/schema", sid)
	assertStatus(t, rec, http.StatusOK, "schema")
	assertBody(t, rec, "items", "schema")

	rec = getWithSession(t, srv, base+"/schema?schema="+testSchema+"&table=items", sid)
	assertStatus(t, rec, http.StatusOK, "schema detail")
	assertBody(t, rec, "PRIMARY KEY", "schema detail")

	rec = getWithSession(t, srv, base+"/data?schema="+testSchema+"&table=items&limit=5", sid)
	assertStatus(t, rec, http.StatusOK, "data")
	assertBody(t, rec, "item 5", "data page 1")
	assertBody(t, rec, `rel="next"`, "data page 1")
	rec = getWithSession(t, srv, base+"/data?schema="+testSchema+"&table=items&limit=5&page=2", sid)
	assertBody(t, rec, "item 7", "data page 2")

	assertStatus(t, getWithSession(t, srv, base+"/schema?schema="+testSchema+"&table=missing", sid), http.StatusNotFound, "missing table")

	rec = postForm(t, srv, base+"/migrations/drafts/explain", sid, csrf, url.Values{"sql": {"select * from " + testSchema + ".items"}})
	assertStatus(t, rec, http.StatusOK, "explain new")
	assertBody(t, rec, "Seq Scan", "explain new")

	rec = postForm(t, srv, base+"/migrations/drafts", sid, csrf, url.Values{"title": {"add column"}, "sql": {"alter table " + testSchema + ".items add column price int;"}})
	assertStatus(t, rec, http.StatusSeeOther, "create draft")
	drafts, err := st.ListMigrationDrafts(ctx, conn.ID)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("drafts = %v, %v", drafts, err)
	}
	draftBase := base + "/migrations/drafts/" + drafts[0].ID.String()
	assertStatus(t, getWithSession(t, srv, draftBase, sid), http.StatusOK, "editor")

	rec = postForm(t, srv, draftBase+"/execute", sid, csrf, url.Values{"token": {"1"}, "title": {"add column"}, "sql": {"alter table " + testSchema + ".items add column price int;"}})
	assertStatus(t, rec, http.StatusUnprocessableEntity, "execute without confirm")

	rec = postForm(t, srv, draftBase+"/execute", sid, csrf, url.Values{"token": {"1"}, "title": {"add column"}, "confirm": {"yes"},
		"sql": {"alter table " + testSchema + ".items add column price int;"}})
	assertStatus(t, rec, http.StatusSeeOther, "execute")
	history, err := st.ListMigrationHistory(ctx, conn.ID, 10)
	if err != nil || len(history) != 1 || history[0].Status != domain.MigrationSuccess {
		t.Fatalf("history = %+v, %v", history, err)
	}
	if drafts, _ := st.ListMigrationDrafts(ctx, conn.ID); len(drafts) != 0 {
		t.Fatalf("executed draft should be removed, got %d", len(drafts))
	}

	rec = postForm(t, srv, base+"/migrations/drafts/execute", sid, csrf, url.Values{"confirm": {"yes"},
		"sql": {"alter table " + testSchema + ".items add column note2 text;\nalter table " + testSchema + ".items add column bogus nosuchtype;"}})
	assertStatus(t, rec, http.StatusUnprocessableEntity, "failed execute")
	assertBody(t, rec, "Execution failed, recorded as v2", "failed execute")
	assertBody(t, rec, `data-error-line="2"`, "failed execute")

	rec = postForm(t, srv, base+"/migrations/drafts", sid, csrf, url.Values{"sql": {"begin;\nselect 1;\ncommit;"}})
	assertStatus(t, rec, http.StatusSeeOther, "save draft with tx control")

	rec = getWithSession(t, srv, base+"/migrations?migration="+history[0].ID.String(), sid)
	assertStatus(t, rec, http.StatusOK, "history detail")
	assertBody(t, rec, "add column price int", "history detail")
	assertStatus(t, getWithSession(t, srv, base+"/migrations/drafts", sid), http.StatusOK, "drafts")
}

func TestDraftSaveConflict(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	user := seedUser(t, st, "budi", "password123", false)
	group, _ := st.CreateGroup(ctx, "backend", "")
	conn := seedDBConnection(t, st, "demo")
	_ = st.AddGroupMember(ctx, group.ID, user.ID)
	_ = st.SetGroupAccess(ctx, group.ID, conn.ID, domain.LevelEdit)
	draft, err := st.CreateMigrationDraft(ctx, domain.MigrationDraft{DBID: conn.ID, SQLText: "select 1", CreatedBy: &user.ID, UpdatedBy: &user.ID})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	sid, csrf := login(t, srv, "budi", "password123")
	path := "/databases/" + conn.ID.String() + "/migrations/drafts/" + draft.ID.String()

	assertStatus(t, postForm(t, srv, path, sid, csrf, url.Values{"token": {"1"}, "sql": {"select 2"}}), http.StatusSeeOther, "first save")
	rec := postForm(t, srv, path, sid, csrf, url.Values{"token": {"1"}, "sql": {"select 3"}})
	assertStatus(t, rec, http.StatusConflict, "stale save")
	assertBody(t, rec, "Someone else changed this draft", "stale save")
}

func TestInactiveUserCannotSignIn(t *testing.T) {
	srv, st := testServer(t)
	admin := seedUser(t, st, "admin", "password123", true)
	user := seedUser(t, st, "eko", "password123", false)
	userSID, _ := login(t, srv, "eko", "password123")
	adminSID, adminCSRF := login(t, srv, "admin", "password123")

	rec := postForm(t, srv, "/admin/users/"+user.ID.String()+"/active", adminSID, adminCSRF, url.Values{"active": {"false"}})
	assertStatus(t, rec, http.StatusSeeOther, "deactivate")
	if rec := getWithSession(t, srv, "/", userSID); rec.Code != http.StatusSeeOther {
		t.Fatalf("deactivated session status = %d; want redirect to login", rec.Code)
	}

	rec = postForm(t, srv, "/admin/users/"+admin.ID.String()+"/active", adminSID, adminCSRF, url.Values{"active": {"false"}})
	assertStatus(t, rec, http.StatusSeeOther, "self deactivate")
	if u, _ := st.GetUserByID(context.Background(), admin.ID); !u.IsActive {
		t.Fatal("admin deactivated themselves")
	}
}
