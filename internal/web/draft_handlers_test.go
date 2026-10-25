package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"leafygo/internal/domain"
)

type draftEnv struct {
	srv      *Server
	conn     domain.DBConnection
	user     domain.User
	editSID  string
	editCSRF string
	base     string
}

// newDraftEnv wires an Edit user to a real target connection created through the admin UI.
func newDraftEnv(t *testing.T) draftEnv {
	t.Helper()
	tc := targetConfig(t)
	srv, st := testServer(t)
	ctx := context.Background()
	seedUser(t, st, "admin", "password123", true)
	user := seedUser(t, st, "budi", "password123", false)
	adminSID, adminCSRF := login(t, srv, "admin", "password123")
	rec := postForm(t, srv, "/admin/databases", adminSID, adminCSRF, url.Values{
		"name": {"Target"}, "host": {tc.Host}, "port": {strconv.Itoa(int(tc.Port))}, "dbname": {tc.Database},
		"username": {tc.User}, "password": {tc.Password}, "sslmode": {"disable"},
	})
	assertStatus(t, rec, http.StatusSeeOther, "create connection")
	conns, _ := st.ListDBConnections(ctx)
	group, _ := st.CreateGroup(ctx, "backend", "")
	_ = st.AddGroupMember(ctx, group.ID, user.ID)
	_ = st.SetGroupAccess(ctx, group.ID, conns[0].ID, domain.LevelEdit)
	sid, csrf := login(t, srv, "budi", "password123")
	return draftEnv{srv: srv, conn: conns[0], user: user, editSID: sid, editCSRF: csrf, base: "/databases/" + conns[0].ID.String()}
}

func (e draftEnv) createDraft(t *testing.T, title, sql string) domain.MigrationDraft {
	t.Helper()
	d, err := e.srv.store.CreateMigrationDraft(context.Background(), domain.MigrationDraft{
		DBID: e.conn.ID, Title: title, SQLText: sql, CreatedBy: &e.user.ID, UpdatedBy: &e.user.ID,
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	return d
}

func TestDraftCreateValidation(t *testing.T) {
	e := newDraftEnv(t)
	drafts := func() int { d, _ := e.srv.store.ListMigrationDrafts(context.Background(), e.conn.ID); return len(d) }

	for name, form := range map[string]url.Values{
		"empty sql":      {"sql": {""}},
		"whitespace sql": {"sql": {"  \n\t "}},
		"huge title":     {"title": {strings.Repeat("t", 5000)}, "sql": {"select 1"}},
		"huge sql":       {"sql": {strings.Repeat("select 1;\n", 200000)}},
	} {
		t.Run(name, func(t *testing.T) {
			assertStatus(t, postForm(t, e.srv, e.base+"/migrations/drafts", e.editSID, e.editCSRF, form), http.StatusUnprocessableEntity, name)
		})
	}
	if drafts() != 0 {
		t.Fatalf("rejected drafts were stored: %d", drafts())
	}
	assertStatus(t, postForm(t, e.srv, e.base+"/migrations/drafts", e.editSID, e.editCSRF, url.Values{"title": {"ok"}, "sql": {"select 1"}}), http.StatusSeeOther, "valid draft")
	if drafts() != 1 {
		t.Fatalf("drafts = %d; want 1", drafts())
	}
	assertStatus(t, getWithSession(t, e.srv, e.base+"/migrations/new", e.editSID), http.StatusOK, "new draft page")
}

func TestDraftDelete(t *testing.T) {
	e := newDraftEnv(t)
	d := e.createDraft(t, "temp", "select 1")
	path := e.base + "/migrations/drafts/" + d.ID.String()

	assertStatus(t, postForm(t, e.srv, path+"/delete", e.editSID, e.editCSRF, nil), http.StatusSeeOther, "delete")
	if left, _ := e.srv.store.ListMigrationDrafts(context.Background(), e.conn.ID); len(left) != 0 {
		t.Fatal("draft not deleted")
	}
	assertStatus(t, getWithSession(t, e.srv, path, e.editSID), http.StatusNotFound, "editor after delete")
	assertStatus(t, postForm(t, e.srv, path+"/delete", e.editSID, e.editCSRF, nil), http.StatusNotFound, "delete twice")
	assertStatus(t, postForm(t, e.srv, e.base+"/migrations/drafts/not-a-uuid/delete", e.editSID, e.editCSRF, nil), http.StatusNotFound, "malformed draft id")
}

func TestDraftExplainSuccessAndFailure(t *testing.T) {
	e := newDraftEnv(t)
	d := e.createDraft(t, "plan", "select 1")
	path := e.base + "/migrations/drafts/" + d.ID.String() + "/explain"
	token := strconv.Itoa(d.Revision)

	rec := postForm(t, e.srv, path, e.editSID, e.editCSRF, url.Values{"token": {token}, "sql": {"select 1"}})
	assertStatus(t, rec, http.StatusOK, "explain")
	assertBody(t, rec, "Result", "explain plan")

	rec = postForm(t, e.srv, path, e.editSID, e.editCSRF, url.Values{"token": {token}, "sql": {"select * from table_that_does_not_exist"}})
	assertStatus(t, rec, http.StatusOK, "explain bad sql")
	assertBody(t, rec, "does not exist", "explain error surfaces target message")

	rec = postForm(t, e.srv, path, e.editSID, e.editCSRF, url.Values{"token": {token}, "sql": {"select 1; select 2"}})
	assertStatus(t, rec, http.StatusOK, "explain multi statement")
	assertBody(t, rec, "one statement", "multi statement rejected")

	rec = postForm(t, e.srv, path, e.editSID, e.editCSRF, url.Values{"token": {"999"}, "sql": {"select 1"}})
	assertStatus(t, rec, http.StatusConflict, "explain with stale token")

	rec = postForm(t, e.srv, path, e.editSID, e.editCSRF, url.Values{"token": {token}, "sql": {""}})
	assertStatus(t, rec, http.StatusUnprocessableEntity, "explain empty sql")

	gone := e.base + "/migrations/drafts/" + uuid.NewString() + "/explain"
	assertStatus(t, postForm(t, e.srv, gone, e.editSID, e.editCSRF, url.Values{"token": {"1"}, "sql": {"select 1"}}), http.StatusNotFound, "explain unknown draft")

	if h, _ := e.srv.store.ListMigrationHistory(context.Background(), e.conn.ID, 10); len(h) != 0 {
		t.Fatalf("explain wrote %d history rows", len(h))
	}
}

func TestDraftExecuteFailureIsRecordedAndKeepsDraft(t *testing.T) {
	e := newDraftEnv(t)
	d := e.createDraft(t, "broken", "select 1/0")
	path := e.base + "/migrations/drafts/" + d.ID.String() + "/execute"

	rec := postForm(t, e.srv, path, e.editSID, e.editCSRF, url.Values{"token": {strconv.Itoa(d.Revision)}, "title": {"broken"}, "sql": {"select 1/0"}, "confirm": {"yes"}})
	assertStatus(t, rec, http.StatusUnprocessableEntity, "failing execute")

	h, err := e.srv.store.ListMigrationHistory(context.Background(), e.conn.ID, 10)
	if err != nil || len(h) != 1 {
		t.Fatalf("history = %+v, %v", h, err)
	}
	if h[0].Status != domain.MigrationFailed || h[0].Version != 1 {
		t.Fatalf("history row = %+v; want failed v1", h[0])
	}
	if h[0].ExecutedBy == nil || *h[0].ExecutedBy != e.user.ID {
		t.Fatalf("history must record the executor, got %v", h[0].ExecutedBy)
	}
	if left, _ := e.srv.store.ListMigrationDrafts(context.Background(), e.conn.ID); len(left) != 1 {
		t.Fatal("a failed migration must keep its draft")
	}
}

func TestDraftExecuteRejectsStaleToken(t *testing.T) {
	e := newDraftEnv(t)
	d := e.createDraft(t, "stale", "select 1")
	path := e.base + "/migrations/drafts/" + d.ID.String() + "/execute"

	rec := postForm(t, e.srv, path, e.editSID, e.editCSRF, url.Values{"token": {"999"}, "sql": {"select 1"}, "confirm": {"yes"}})
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale execute status = %d; want 409", rec.Code)
	}
	if h, _ := e.srv.store.ListMigrationHistory(context.Background(), e.conn.ID, 10); len(h) != 0 {
		t.Fatal("a stale execute must not write history")
	}
}

func TestDraftSaveAndExplainRejectBlockedStatements(t *testing.T) {
	e := newDraftEnv(t)
	for name, sql := range map[string]string{
		"transaction control": "begin; select 1; commit;",
		"vacuum":              "vacuum",
		"copy program":        "copy t to program 'rm -rf /'",
	} {
		t.Run(name, func(t *testing.T) {
			rec := postForm(t, e.srv, e.base+"/migrations/drafts/execute", e.editSID, e.editCSRF, url.Values{"sql": {sql}, "confirm": {"yes"}})
			if rec.Code == http.StatusSeeOther {
				t.Fatalf("%q was executed", sql)
			}
			if h, _ := e.srv.store.ListMigrationHistory(context.Background(), e.conn.ID, 10); len(h) != 0 {
				for _, row := range h {
					if row.Status == domain.MigrationSuccess {
						t.Fatalf("%q committed: %+v", sql, row)
					}
				}
			}
		})
	}
}

func TestViewOnlyBlockedOnDraftSpecificRoutes(t *testing.T) {
	e := newDraftEnv(t)
	ctx := context.Background()
	st := e.srv.store
	viewer := seedUser(t, st, "ega", "password123", false)
	group, _ := st.CreateGroup(ctx, "readers", "")
	_ = st.AddGroupMember(ctx, group.ID, viewer.ID)
	_ = st.SetGroupAccess(ctx, group.ID, e.conn.ID, domain.LevelView)
	sid, csrf := login(t, e.srv, "ega", "password123")

	d := e.createDraft(t, "secret plan", "select secret_column from hidden")
	path := e.base + "/migrations/drafts/" + d.ID.String()
	for _, p := range []string{path, path + "/delete", path + "/explain", path + "/execute"} {
		rec := postForm(t, e.srv, p, sid, csrf, url.Values{"token": {"1"}, "sql": {"select 1"}, "confirm": {"yes"}})
		if p == path {
			rec = getWithSession(t, e.srv, p, sid)
		}
		assertStatus(t, rec, http.StatusForbidden, p)
		if strings.Contains(rec.Body.String(), "secret_column") {
			t.Fatalf("%s leaked draft SQL to a View Only user", p)
		}
	}
	if left, _ := st.ListMigrationDrafts(ctx, e.conn.ID); len(left) != 1 {
		t.Fatal("a View Only user managed to delete a draft")
	}
	if h, _ := st.ListMigrationHistory(ctx, e.conn.ID, 10); len(h) != 0 {
		t.Fatal("a View Only user managed to execute a migration")
	}
}

func TestViewOnlyCanReadHistoryButEditorOnlySeesOwnDatabase(t *testing.T) {
	e := newDraftEnv(t)
	ctx := context.Background()
	st := e.srv.store
	viewer := seedUser(t, st, "ega", "password123", false)
	group, _ := st.CreateGroup(ctx, "readers", "")
	_ = st.AddGroupMember(ctx, group.ID, viewer.ID)
	_ = st.SetGroupAccess(ctx, group.ID, e.conn.ID, domain.LevelView)
	sid, _ := login(t, e.srv, "ega", "password123")

	rec := postForm(t, e.srv, e.base+"/migrations/drafts/execute", e.editSID, e.editCSRF, url.Values{"confirm": {"yes"}, "sql": {"select 42 as answer"}})
	assertStatus(t, rec, http.StatusSeeOther, "editor executes")
	h, _ := st.ListMigrationHistory(ctx, e.conn.ID, 10)
	if len(h) != 1 {
		t.Fatalf("history = %d; want 1", len(h))
	}

	rec = getWithSession(t, e.srv, e.base+"/migrations?migration="+h[0].ID.String(), sid)
	assertStatus(t, rec, http.StatusOK, "viewer reads history detail")
	assertBody(t, rec, "select 42 as answer", "history detail shows executed SQL")

	other := seedDBConnection(t, st, "other")
	for _, p := range []string{"/schema", "/data", "/migrations", "/"} {
		rec := getWithSession(t, e.srv, "/databases/"+other.ID.String()+p, e.editSID)
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
			t.Errorf("GET other db %s = %d; want 403 or 404", p, rec.Code)
		}
	}
}

func TestDraftIsolatedBetweenDatabases(t *testing.T) {
	e := newDraftEnv(t)
	ctx := context.Background()
	st := e.srv.store
	other := seedDBConnection(t, st, "other")
	groups, _ := st.ListGroups(ctx)
	_ = st.SetGroupAccess(ctx, groups[0].ID, other.ID, domain.LevelEdit)
	sid, csrf := login(t, e.srv, "budi", "password123")

	d := e.createDraft(t, "mine", "select 1")
	wrong := "/databases/" + other.ID.String() + "/migrations/drafts/" + d.ID.String()
	assertStatus(t, getWithSession(t, e.srv, wrong, sid), http.StatusNotFound, "open via wrong database")
	assertStatus(t, postForm(t, e.srv, wrong+"/delete", sid, csrf, nil), http.StatusNotFound, "delete via wrong database")
	if left, _ := st.ListMigrationDrafts(ctx, e.conn.ID); len(left) != 1 {
		t.Fatal("a draft was deleted through another database's URL")
	}
}
