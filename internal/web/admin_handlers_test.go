package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"leafygo/internal/auth"
	"leafygo/internal/domain"
)

func sessionRevoked(t *testing.T, srv *Server, sid string) bool {
	t.Helper()
	sess, err := srv.store.GetSession(context.Background(), auth.HashToken(sid))
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	return sess.RevokedAt != nil
}

func TestAdminPasswordReset(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "admin", "password123", true)
	user := seedUser(t, st, "budi", "password123", false)
	userSID, _ := login(t, srv, "budi", "password123")
	adminSID, adminCSRF := login(t, srv, "admin", "password123")
	path := "/admin/users/" + user.ID.String() + "/password"

	for _, bad := range []string{"", "short", strings.Repeat("x", 2000)} {
		rec := postForm(t, srv, path, adminSID, adminCSRF, url.Values{"password": {bad}})
		assertStatus(t, rec, http.StatusUnprocessableEntity, "invalid password")
	}
	if sessionRevoked(t, srv, userSID) {
		t.Fatal("rejected reset must not sign the user out")
	}

	assertStatus(t, postForm(t, srv, path, adminSID, adminCSRF, url.Values{"password": {"brand-new-pass"}}), http.StatusSeeOther, "reset")
	if !sessionRevoked(t, srv, userSID) {
		t.Fatal("password reset must revoke the user's sessions")
	}
	u, _ := st.GetUserByID(context.Background(), user.ID)
	if ok, _ := auth.VerifyPassword(u.PasswordHash, "brand-new-pass"); !ok {
		t.Fatal("new password does not verify")
	}
	if ok, _ := auth.VerifyPassword(u.PasswordHash, "password123"); ok {
		t.Fatal("old password still verifies")
	}
	if strings.Contains(u.PasswordHash, "brand-new-pass") {
		t.Fatal("password stored in plaintext")
	}

	missing := "/admin/users/" + uuid.NewString() + "/password"
	assertStatus(t, postForm(t, srv, missing, adminSID, adminCSRF, url.Values{"password": {"brand-new-pass"}}), http.StatusNotFound, "unknown user")
	assertStatus(t, postForm(t, srv, "/admin/users/not-a-uuid/password", adminSID, adminCSRF, url.Values{"password": {"brand-new-pass"}}), http.StatusNotFound, "malformed id")
}

func TestAdminResetOwnPasswordSignsOut(t *testing.T) {
	srv, st := testServer(t)
	admin := seedUser(t, st, "admin", "password123", true)
	sid, csrf := login(t, srv, "admin", "password123")

	rec := postForm(t, srv, "/admin/users/"+admin.ID.String()+"/password", sid, csrf, url.Values{"password": {"brand-new-pass"}})
	assertStatus(t, rec, http.StatusSeeOther, "own reset")
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("redirect = %q; want /login", loc)
	}
	if !sessionRevoked(t, srv, sid) {
		t.Fatal("own session must be revoked after a password change")
	}
}

func TestAdminCreateUserValidation(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "admin", "password123", true)
	seedUser(t, st, "budi", "password123", false)
	sid, csrf := login(t, srv, "admin", "password123")

	tests := []struct {
		name string
		form url.Values
	}{
		{"duplicate username", url.Values{"username": {"budi"}, "password": {"password123"}}},
		{"duplicate differing by case", url.Values{"username": {"BUDI"}, "password": {"password123"}}},
		{"short password", url.Values{"username": {"fresh"}, "password": {"short"}}},
		{"invalid username", url.Values{"username": {"has space"}, "password": {"password123"}}},
		{"empty username", url.Values{"username": {""}, "password": {"password123"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertStatus(t, postForm(t, srv, "/admin/users", sid, csrf, tt.form), http.StatusUnprocessableEntity, tt.name)
		})
	}
	users, _ := st.ListUsers(context.Background())
	if len(users) != 2 {
		t.Fatalf("users after rejected creates = %d; want 2", len(users))
	}
}

func TestAdminActiveToggleRoundTrip(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "admin", "password123", true)
	user := seedUser(t, st, "eko", "password123", false)
	adminSID, adminCSRF := login(t, srv, "admin", "password123")
	path := "/admin/users/" + user.ID.String() + "/active"

	assertStatus(t, postForm(t, srv, path, adminSID, adminCSRF, url.Values{"active": {"false"}}), http.StatusSeeOther, "deactivate")
	if rec := attemptLogin(srv, "eko", "password123"); responseCookie(rec, "sid") != nil {
		t.Fatal("deactivated user could sign in")
	}
	assertStatus(t, postForm(t, srv, path, adminSID, adminCSRF, url.Values{"active": {"true"}}), http.StatusSeeOther, "activate")
	if rec := attemptLogin(srv, "eko", "password123"); responseCookie(rec, "sid") == nil {
		t.Fatal("reactivated user could not sign in")
	}
	assertStatus(t, postForm(t, srv, "/admin/users/"+uuid.NewString()+"/active", adminSID, adminCSRF, url.Values{"active": {"false"}}), http.StatusNotFound, "unknown user")
}

func TestAdminGroupCreateValidationAndUpdate(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "admin", "password123", true)
	adminSID, adminCSRF := login(t, srv, "admin", "password123")
	ctx := context.Background()

	assertStatus(t, postForm(t, srv, "/admin/groups", adminSID, adminCSRF, url.Values{"name": {"backend"}}), http.StatusSeeOther, "create")
	assertStatus(t, postForm(t, srv, "/admin/groups", adminSID, adminCSRF, url.Values{"name": {"backend"}}), http.StatusUnprocessableEntity, "duplicate")
	assertStatus(t, postForm(t, srv, "/admin/groups", adminSID, adminCSRF, url.Values{"name": {""}}), http.StatusUnprocessableEntity, "empty name")
	assertStatus(t, getWithSession(t, srv, "/admin/groups/new", adminSID), http.StatusOK, "new form")

	backend, _ := st.GetGroupByName(ctx, "backend")
	assertStatus(t, postForm(t, srv, "/admin/groups", adminSID, adminCSRF, url.Values{"name": {"frontend"}}), http.StatusSeeOther, "second group")

	path := "/admin/groups/" + backend.ID.String()
	assertStatus(t, postForm(t, srv, path, adminSID, adminCSRF, url.Values{"name": {"platform"}, "description": {"core team"}}), http.StatusSeeOther, "rename")
	g, _ := st.GetGroup(ctx, backend.ID)
	if g.Name != "platform" || g.Description != "core team" {
		t.Fatalf("group after update = %+v", g)
	}
	assertStatus(t, postForm(t, srv, path, adminSID, adminCSRF, url.Values{"name": {"frontend"}}), http.StatusUnprocessableEntity, "rename onto existing")
	assertStatus(t, postForm(t, srv, path, adminSID, adminCSRF, url.Values{"name": {""}}), http.StatusUnprocessableEntity, "rename to empty")
	assertStatus(t, getWithSession(t, srv, "/admin/groups/"+uuid.NewString(), adminSID), http.StatusNotFound, "unknown group")
}

func TestAdminGroupMembership(t *testing.T) {
	srv, st := testServer(t)
	admin := seedUser(t, st, "admin", "password123", true)
	user := seedUser(t, st, "budi", "password123", false)
	group, _ := st.CreateGroup(context.Background(), "backend", "")
	adminSID, adminCSRF := login(t, srv, "admin", "password123")
	userSID, _ := login(t, srv, "budi", "password123")
	path := "/admin/groups/" + group.ID.String() + "/members"
	members := func() int { m, _ := st.ListGroupMembers(context.Background(), group.ID); return len(m) }

	assertStatus(t, postForm(t, srv, path, adminSID, adminCSRF, url.Values{"action": {"add"}, "user_id": {user.ID.String()}}), http.StatusSeeOther, "add")
	if members() != 1 {
		t.Fatalf("members = %d; want 1", members())
	}

	rec := postForm(t, srv, path, adminSID, adminCSRF, url.Values{"action": {"add"}, "user_id": {admin.ID.String()}})
	assertStatus(t, rec, http.StatusSeeOther, "add admin")
	if members() != 1 {
		t.Fatal("a Platform Admin must not be added to a group")
	}
	assertStatus(t, postForm(t, srv, path, adminSID, adminCSRF, url.Values{"action": {"add"}, "user_id": {uuid.NewString()}}), http.StatusSeeOther, "add unknown user")
	assertStatus(t, postForm(t, srv, path, adminSID, adminCSRF, url.Values{"action": {"add"}}), http.StatusSeeOther, "add without user")
	assertStatus(t, postForm(t, srv, path, adminSID, adminCSRF, url.Values{"action": {"explode"}, "user_id": {user.ID.String()}}), http.StatusBadRequest, "unknown action")

	if sessionRevoked(t, srv, userSID) {
		t.Fatal("adding a member must not sign the user out")
	}
	assertStatus(t, postForm(t, srv, path, adminSID, adminCSRF, url.Values{"action": {"remove"}, "user_id": {user.ID.String()}}), http.StatusSeeOther, "remove")
	if members() != 0 {
		t.Fatal("member not removed")
	}
	if !sessionRevoked(t, srv, userSID) {
		t.Fatal("removing a member must revoke their sessions")
	}
}

func TestAdminGroupAccessChangesAffectSessions(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seedUser(t, st, "admin", "password123", true)
	user := seedUser(t, st, "budi", "password123", false)
	group, _ := st.CreateGroup(ctx, "backend", "")
	conn := seedDBConnection(t, st, "demo")
	_ = st.AddGroupMember(ctx, group.ID, user.ID)
	adminSID, adminCSRF := login(t, srv, "admin", "password123")
	path := "/admin/groups/" + group.ID.String() + "/access"
	grant := func(level string) *httptest.ResponseRecorder {
		return postForm(t, srv, path, adminSID, adminCSRF, url.Values{"db_id": {conn.ID.String()}, "level": {level}})
	}

	userSID, _ := login(t, srv, "budi", "password123")
	assertStatus(t, grant("view"), http.StatusSeeOther, "grant view")
	if lvl, _ := st.EffectiveLevel(ctx, user.ID, conn.ID); lvl != domain.LevelView {
		t.Fatalf("level = %v; want view", lvl)
	}
	if sessionRevoked(t, srv, userSID) {
		t.Fatal("an upgrade must rotate sessions, not revoke them")
	}

	assertStatus(t, grant("edit"), http.StatusSeeOther, "upgrade to edit")
	if lvl, _ := st.EffectiveLevel(ctx, user.ID, conn.ID); lvl != domain.LevelEdit {
		t.Fatalf("level = %v; want edit", lvl)
	}

	assertStatus(t, grant("view"), http.StatusSeeOther, "downgrade to view")
	if !sessionRevoked(t, srv, userSID) {
		t.Fatal("a downgrade must revoke the members' sessions")
	}

	newSID, newCSRF := login(t, srv, "budi", "password123")
	base := "/databases/" + conn.ID.String()
	assertStatus(t, postForm(t, srv, base+"/migrations/drafts", newSID, newCSRF, url.Values{"sql": {"select 1"}}), http.StatusForbidden, "edit after downgrade")

	assertStatus(t, grant("none"), http.StatusSeeOther, "revoke")
	if lvl, _ := st.EffectiveLevel(ctx, user.ID, conn.ID); lvl != domain.LevelNone {
		t.Fatalf("level after revoke = %v; want none", lvl)
	}
	lastSID, _ := login(t, srv, "budi", "password123")
	if rec := getWithSession(t, srv, base+"/schema", lastSID); rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("schema after revoke = %d; want 403 or 404", rec.Code)
	}

	assertStatus(t, postForm(t, srv, path, adminSID, adminCSRF, url.Values{"db_id": {uuid.NewString()}, "level": {"view"}}), http.StatusSeeOther, "grant on unknown db")
	if grants, _ := st.ListGroupAccess(ctx, group.ID); len(grants) != 0 {
		t.Fatalf("grant on an unknown database was stored: %+v", grants)
	}
}

func TestAdminGroupDeleteRevokesMembersAndAccess(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seedUser(t, st, "admin", "password123", true)
	user := seedUser(t, st, "budi", "password123", false)
	group, _ := st.CreateGroup(ctx, "backend", "")
	conn := seedDBConnection(t, st, "demo")
	_ = st.AddGroupMember(ctx, group.ID, user.ID)
	_ = st.SetGroupAccess(ctx, group.ID, conn.ID, domain.LevelEdit)
	adminSID, adminCSRF := login(t, srv, "admin", "password123")
	userSID, _ := login(t, srv, "budi", "password123")

	assertStatus(t, postForm(t, srv, "/admin/groups/"+group.ID.String()+"/delete", adminSID, adminCSRF, nil), http.StatusSeeOther, "delete")
	if _, err := st.GetGroup(ctx, group.ID); err == nil {
		t.Fatal("group still exists")
	}
	if lvl, _ := st.EffectiveLevel(ctx, user.ID, conn.ID); lvl != domain.LevelNone {
		t.Fatalf("access survived group deletion: %v", lvl)
	}
	if !sessionRevoked(t, srv, userSID) {
		t.Fatal("deleting a group must revoke its members' sessions")
	}
	assertStatus(t, postForm(t, srv, "/admin/groups/"+group.ID.String()+"/delete", adminSID, adminCSRF, nil), http.StatusNotFound, "delete twice")
}

func dbFormValues(tc map[string]string, over map[string]string) url.Values {
	v := url.Values{}
	for k, val := range tc {
		v.Set(k, val)
	}
	for k, val := range over {
		v.Set(k, val)
	}
	return v
}

func TestAdminDatabaseCreateUpdateDelete(t *testing.T) {
	tc := targetConfig(t)
	srv, st := testServer(t)
	ctx := context.Background()
	seedUser(t, st, "admin", "password123", true)
	adminSID, adminCSRF := login(t, srv, "admin", "password123")
	base := map[string]string{
		"name": "Target", "host": tc.Host, "port": strconv.Itoa(int(tc.Port)), "dbname": tc.Database,
		"username": tc.User, "password": "zq9-sentinel-secret", "sslmode": "disable",
	}

	invalid := []map[string]string{
		{"name": ""}, {"host": ""}, {"port": "0"}, {"port": "70000"}, {"port": "abc"},
		{"dbname": ""}, {"username": ""}, {"password": ""}, {"sslmode": "bogus"},
	}
	for _, over := range invalid {
		rec := postForm(t, srv, "/admin/databases", adminSID, adminCSRF, dbFormValues(base, over))
		assertStatus(t, rec, http.StatusUnprocessableEntity, "invalid connection")
	}
	if conns, _ := st.ListDBConnections(ctx); len(conns) != 0 {
		t.Fatalf("invalid forms created %d connections", len(conns))
	}

	rec := postForm(t, srv, "/admin/databases", adminSID, adminCSRF, dbFormValues(base, nil))
	assertStatus(t, rec, http.StatusSeeOther, "create")
	assertStatus(t, postForm(t, srv, "/admin/databases", adminSID, adminCSRF, dbFormValues(base, nil)), http.StatusUnprocessableEntity, "duplicate name")
	conns, _ := st.ListDBConnections(ctx)
	if len(conns) != 1 {
		t.Fatalf("connections = %d; want 1", len(conns))
	}
	conn := conns[0]
	if strings.Contains(string(conn.PasswordEnc), "zq9-sentinel-secret") {
		t.Fatal("connection password stored in plaintext")
	}

	path := "/admin/databases/" + conn.ID.String()
	page := getWithSession(t, srv, path, adminSID)
	assertStatus(t, page, http.StatusOK, "edit form")
	if strings.Contains(page.Body.String(), "zq9-sentinel-secret") {
		t.Fatal("edit form echoes the stored connection password")
	}

	assertStatus(t, postForm(t, srv, path, adminSID, adminCSRF, dbFormValues(base, map[string]string{"name": "Renamed", "password": ""})), http.StatusSeeOther, "update keeping password")
	updated, _ := st.GetDBConnection(ctx, conn.ID)
	if updated.Name != "Renamed" || string(updated.PasswordEnc) != string(conn.PasswordEnc) {
		t.Fatalf("blank password should keep the stored secret: %+v", updated)
	}
	assertStatus(t, postForm(t, srv, path, adminSID, adminCSRF, dbFormValues(base, map[string]string{"password": "rotated"})), http.StatusSeeOther, "update with password")
	rotated, _ := st.GetDBConnection(ctx, conn.ID)
	if string(rotated.PasswordEnc) == string(conn.PasswordEnc) {
		t.Fatal("a new password must replace the stored secret")
	}

	assertStatus(t, postForm(t, srv, "/admin/databases", adminSID, adminCSRF, dbFormValues(base, map[string]string{"name": "Other"})), http.StatusSeeOther, "second connection")
	assertStatus(t, postForm(t, srv, path, adminSID, adminCSRF, dbFormValues(base, map[string]string{"name": "Other"})), http.StatusUnprocessableEntity, "rename onto existing")
	assertStatus(t, postForm(t, srv, "/admin/databases/"+uuid.NewString(), adminSID, adminCSRF, dbFormValues(base, nil)), http.StatusNotFound, "update unknown")

	assertStatus(t, postForm(t, srv, path+"/delete", adminSID, adminCSRF, nil), http.StatusSeeOther, "delete")
	if _, err := st.GetDBConnection(ctx, conn.ID); err == nil {
		t.Fatal("connection still exists")
	}
	assertStatus(t, postForm(t, srv, path+"/delete", adminSID, adminCSRF, nil), http.StatusNotFound, "delete twice")
}

func TestAdminDatabaseTestReportsFailure(t *testing.T) {
	srv, st := testServer(t)
	seedUser(t, st, "admin", "password123", true)
	conn := seedDBConnection(t, st, "unreachable")
	if _, err := st.Pool().Exec(context.Background(), `update db_connections set host='127.0.0.1', port=1 where id=$1`, conn.ID); err != nil {
		t.Fatalf("repoint connection: %v", err)
	}
	sid, csrf := login(t, srv, "admin", "password123")

	rec := postForm(t, srv, "/admin/databases/"+conn.ID.String()+"/test", sid, csrf, nil)
	assertStatus(t, rec, http.StatusSeeOther, "test unreachable")
	if c := responseCookie(rec, "flash"); c == nil || !strings.HasPrefix(c.Value, "error|") {
		t.Fatalf("a failed connection test must flash an error, got %+v", c)
	}
	assertStatus(t, postForm(t, srv, "/admin/databases/"+uuid.NewString()+"/test", sid, csrf, nil), http.StatusNotFound, "test unknown")
}

func TestNonAdminCannotMutateAdminResources(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seedUser(t, st, "budi", "password123", false)
	victim := seedUser(t, st, "ega", "password123", false)
	group, _ := st.CreateGroup(ctx, "backend", "")
	conn := seedDBConnection(t, st, "demo")
	sid, csrf := login(t, srv, "budi", "password123")

	posts := []string{
		"/admin/users/" + victim.ID.String() + "/password",
		"/admin/users/" + victim.ID.String() + "/active",
		"/admin/groups/" + group.ID.String(),
		"/admin/groups/" + group.ID.String() + "/members",
		"/admin/groups/" + group.ID.String() + "/access",
		"/admin/groups/" + group.ID.String() + "/delete",
		"/admin/databases/" + conn.ID.String(),
		"/admin/databases/" + conn.ID.String() + "/test",
		"/admin/databases/" + conn.ID.String() + "/delete",
	}
	for _, path := range posts {
		form := url.Values{"password": {"hijacked-pass"}, "active": {"false"}, "name": {"hijacked"}}
		assertStatus(t, postForm(t, srv, path, sid, csrf, form), http.StatusForbidden, "POST "+path)
	}
	for _, path := range []string{"/admin/groups/new", "/admin/databases/new", "/admin/groups/" + group.ID.String(), "/admin/databases/" + conn.ID.String()} {
		assertStatus(t, getWithSession(t, srv, path, sid), http.StatusForbidden, "GET "+path)
	}

	if g, _ := st.GetGroup(ctx, group.ID); g.Name != "backend" {
		t.Fatal("a forbidden request still modified the group")
	}
	if u, _ := st.GetUserByID(ctx, victim.ID); !u.IsActive {
		t.Fatal("a forbidden request still deactivated a user")
	}
	if _, err := st.GetDBConnection(ctx, conn.ID); err != nil {
		t.Fatal("a forbidden request still deleted the connection")
	}
}
