package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"leafygo/internal/auth"
	"leafygo/internal/domain"
	"leafygo/internal/store"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{3,64}$`)

var sslModes = map[string]bool{
	"disable": true, "allow": true, "prefer": true, "require": true, "verify-ca": true, "verify-full": true,
}

const (
	minPasswordLen = 8
	maxPasswordLen = 256
	maxNameLen     = 100
	maxDescLen     = 500
)

func (s *Server) redirectFlash(w http.ResponseWriter, r *http.Request, to, kind, message string) {
	s.setFlash(w, kind, message)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	s.logger.Error(what, "error", err)
	s.renderError(w, r, http.StatusInternalServerError, "Something went wrong. Try again.")
}

func urlID(r *http.Request, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, param))
	return id, err == nil
}

func formID(r *http.Request, field string) (uuid.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(r.FormValue(field)))
	return id, err == nil
}

func validatePassword(password string) string {
	switch {
	case utf8.RuneCountInString(password) < minPasswordLen:
		return "Password must be at least 8 characters."
	case len(password) > maxPasswordLen:
		return "Password must be at most 256 characters."
	}
	return ""
}

// Users

func (s *Server) adminUsersView(ctx context.Context, editID uuid.UUID, form formState) (adminUsersView, error) {
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		return adminUsersView{}, err
	}
	groups, err := s.store.ListGroups(ctx)
	if err != nil {
		return adminUsersView{}, err
	}
	memberOf := map[uuid.UUID][]groupOption{}
	for _, g := range groups {
		members, err := s.store.ListGroupMembers(ctx, g.ID)
		if err != nil {
			return adminUsersView{}, err
		}
		for _, m := range members {
			memberOf[m.ID] = append(memberOf[m.ID], groupOption{ID: g.ID, Name: g.Name})
		}
	}
	activeAdmins, err := s.store.CountActivePlatformAdmins(ctx)
	if err != nil {
		return adminUsersView{}, err
	}
	v := adminUsersView{ActiveAdmins: activeAdmins, Form: form}
	for _, u := range users {
		v.Users = append(v.Users, adminUserRow{
			ID: u.ID, Username: u.Username, IsPlatformAdmin: u.IsPlatformAdmin, IsActive: u.IsActive,
			Groups: memberOf[u.ID], CreatedAt: u.CreatedAt,
		})
	}
	for i := range v.Users {
		if v.Users[i].ID == editID {
			v.Editing = &v.Users[i]
		}
	}
	return v, nil
}

func (s *Server) renderAdminUsers(w http.ResponseWriter, r *http.Request, status int, editID uuid.UUID, form formState) {
	v, err := s.adminUsersView(r.Context(), editID, form)
	if err != nil {
		s.serverError(w, r, "admin users view", err)
		return
	}
	s.render(w, r, status, "admin/users.html", pageData{Title: "Users", Nav: navAdminUsers, Data: v})
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	editID, _ := uuid.Parse(r.URL.Query().Get("edit"))
	s.renderAdminUsers(w, r, http.StatusOK, editID, formState{})
}

func (s *Server) handleAdminUserCreate(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	isAdmin := r.FormValue("is_admin") == "true"

	errs := map[string]string{}
	if !usernamePattern.MatchString(username) {
		errs["username"] = "Use 3 to 64 letters, digits, dots, underscores, or hyphens."
	}
	if msg := validatePassword(password); msg != "" {
		errs["password"] = msg
	}
	values := map[string]string{"username": username, "is_admin": strconv.FormatBool(isAdmin)}
	if len(errs) > 0 {
		s.renderAdminUsers(w, r, http.StatusUnprocessableEntity, uuid.Nil, formState{Values: values, Errors: errs})
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		s.serverError(w, r, "hash password", err)
		return
	}
	u, err := s.store.CreateUser(r.Context(), username, hash, isAdmin)
	if errors.Is(err, store.ErrConflict) {
		errs["username"] = "This username is already taken."
		s.renderAdminUsers(w, r, http.StatusUnprocessableEntity, uuid.Nil, formState{Values: values, Errors: errs})
		return
	}
	if err != nil {
		s.serverError(w, r, "create user", err)
		return
	}
	s.redirectFlash(w, r, "/admin/users?edit="+u.ID.String()+"#user-form", "success", "User "+u.Username+" created.")
}

func (s *Server) handleAdminUserPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := urlID(r, "userID")
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "User not found.")
		return
	}
	password := r.FormValue("password")
	if msg := validatePassword(password); msg != "" {
		s.renderAdminUsers(w, r, http.StatusUnprocessableEntity, id, formState{Errors: map[string]string{"password": msg}})
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		s.serverError(w, r, "hash password", err)
		return
	}
	if err := s.store.UpdateUserPassword(r.Context(), id, hash); errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "User not found.")
		return
	} else if err != nil {
		s.serverError(w, r, "update password", err)
		return
	}
	if err := s.store.RevokeUserSessions(r.Context(), id); err != nil {
		s.serverError(w, r, "revoke sessions", err)
		return
	}
	if id == currentUser(r).ID {
		s.clearSessionCookie(w)
		s.redirectFlash(w, r, "/login", "success", "Password changed. Sign in with your new password.")
		return
	}
	s.redirectFlash(w, r, "/admin/users?edit="+id.String()+"#user-form", "success", "Password reset. The user was signed out everywhere.")
}

func (s *Server) handleAdminUserActive(w http.ResponseWriter, r *http.Request) {
	id, ok := urlID(r, "userID")
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "User not found.")
		return
	}
	active := r.FormValue("active") == "true"
	back := "/admin/users?edit=" + id.String() + "#user-form"
	err := s.store.SetUserActiveGuarded(r.Context(), id, active, currentUser(r).ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.renderError(w, r, http.StatusNotFound, "User not found.")
		return
	case errors.Is(err, store.ErrSelfMutation):
		s.redirectFlash(w, r, back, "error", "You cannot deactivate your own account.")
		return
	case errors.Is(err, store.ErrLastAdmin):
		s.redirectFlash(w, r, back, "error", "The last active admin cannot be deactivated.")
		return
	case err != nil:
		s.serverError(w, r, "set user active", err)
		return
	}
	if !active {
		if err := s.store.RevokeUserSessions(r.Context(), id); err != nil {
			s.serverError(w, r, "revoke sessions", err)
			return
		}
		s.redirectFlash(w, r, back, "success", "User deactivated and signed out.")
		return
	}
	s.redirectFlash(w, r, back, "success", "User activated.")
}

// Groups

func (s *Server) groupRow(ctx context.Context, g domain.Group) (adminGroupRow, error) {
	row := adminGroupRow{ID: g.ID, Name: g.Name, Description: g.Description}
	members, err := s.store.ListGroupMembers(ctx, g.ID)
	if err != nil {
		return row, err
	}
	for _, m := range members {
		row.Members = append(row.Members, userOption{ID: m.ID, Username: m.Username})
	}
	access, err := s.store.ListGroupAccess(ctx, g.ID)
	if err != nil {
		return row, err
	}
	for _, a := range access {
		row.Access = append(row.Access, groupAccessRow{DBID: a.DBID, DBName: a.DBName, Level: a.Level})
	}
	return row, nil
}

func (s *Server) handleAdminGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.ListGroups(r.Context())
	if err != nil {
		s.serverError(w, r, "list groups", err)
		return
	}
	var v adminGroupsView
	for _, g := range groups {
		row, err := s.groupRow(r.Context(), g)
		if err != nil {
			s.serverError(w, r, "group row", err)
			return
		}
		v.Groups = append(v.Groups, row)
	}
	s.render(w, r, http.StatusOK, "admin/groups.html", pageData{Title: "Groups", Nav: navAdminGroups, Data: v})
}

func (s *Server) handleAdminGroupNew(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "admin/group_new.html", pageData{Title: "Create group", Nav: navAdminGroups, Data: adminGroupNewView{}})
}

func groupForm(r *http.Request) (string, string, formState) {
	name := strings.TrimSpace(r.FormValue("name"))
	desc := strings.TrimSpace(r.FormValue("description"))
	form := formState{Values: map[string]string{"name": name, "description": desc}, Errors: map[string]string{}}
	switch {
	case name == "":
		form.Errors["name"] = "Group name is required."
	case utf8.RuneCountInString(name) > maxNameLen:
		form.Errors["name"] = "Group name must be at most 100 characters."
	}
	if utf8.RuneCountInString(desc) > maxDescLen {
		form.Errors["description"] = "Description must be at most 500 characters."
		form.Errors["form"] = form.Errors["description"]
	}
	return name, desc, form
}

func (s *Server) handleAdminGroupCreate(w http.ResponseWriter, r *http.Request) {
	name, desc, form := groupForm(r)
	render := func() {
		s.render(w, r, http.StatusUnprocessableEntity, "admin/group_new.html", pageData{Title: "Create group", Nav: navAdminGroups, Data: adminGroupNewView{Form: form}})
	}
	if len(form.Errors) > 0 {
		render()
		return
	}
	g, err := s.store.CreateGroup(r.Context(), name, desc)
	if errors.Is(err, store.ErrConflict) {
		form.Errors["name"] = "A group with this name already exists."
		render()
		return
	}
	if err != nil {
		s.serverError(w, r, "create group", err)
		return
	}
	s.redirectFlash(w, r, "/admin/groups/"+g.ID.String(), "success", "Group "+g.Name+" created.")
}

func (s *Server) loadGroup(w http.ResponseWriter, r *http.Request) (domain.Group, bool) {
	id, ok := urlID(r, "groupID")
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "Group not found.")
		return domain.Group{}, false
	}
	g, err := s.store.GetGroup(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "Group not found.")
		return domain.Group{}, false
	}
	if err != nil {
		s.serverError(w, r, "get group", err)
		return domain.Group{}, false
	}
	return g, true
}

func (s *Server) renderAdminGroup(w http.ResponseWriter, r *http.Request, status int, g domain.Group, form *formState) {
	ctx := r.Context()
	row, err := s.groupRow(ctx, g)
	if err != nil {
		s.serverError(w, r, "group row", err)
		return
	}
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		s.serverError(w, r, "list users", err)
		return
	}
	var userOpts []userOption
	for _, u := range users {
		if !u.IsPlatformAdmin {
			userOpts = append(userOpts, userOption{ID: u.ID, Username: u.Username})
		}
	}
	conns, err := s.store.ListDBConnections(ctx)
	if err != nil {
		s.serverError(w, r, "list databases", err)
		return
	}
	var dbOpts []dbOption
	for _, c := range conns {
		dbOpts = append(dbOpts, dbOption{ID: c.ID, Name: c.Name})
	}
	v := adminGroupView{Group: row, Form: formState{Values: groupFormValues(row)}}
	if form != nil {
		v.Form = *form
	}
	v.Users, v.Databases = groupChoices(row, userOpts, dbOpts)
	s.render(w, r, status, "admin/group_edit.html", pageData{Title: g.Name, Nav: navAdminGroups, Data: v})
}

func (s *Server) handleAdminGroup(w http.ResponseWriter, r *http.Request) {
	if g, ok := s.loadGroup(w, r); ok {
		s.renderAdminGroup(w, r, http.StatusOK, g, nil)
	}
}

func (s *Server) handleAdminGroupUpdate(w http.ResponseWriter, r *http.Request) {
	g, ok := s.loadGroup(w, r)
	if !ok {
		return
	}
	name, desc, form := groupForm(r)
	if len(form.Errors) > 0 {
		s.renderAdminGroup(w, r, http.StatusUnprocessableEntity, g, &form)
		return
	}
	updated, err := s.store.UpdateGroup(r.Context(), g.ID, name, desc)
	if errors.Is(err, store.ErrConflict) {
		form.Errors["name"] = "A group with this name already exists."
		s.renderAdminGroup(w, r, http.StatusUnprocessableEntity, g, &form)
		return
	}
	if err != nil {
		s.serverError(w, r, "update group", err)
		return
	}
	s.redirectFlash(w, r, "/admin/groups/"+g.ID.String(), "success", "Group "+updated.Name+" saved.")
}

func (s *Server) handleAdminGroupMembers(w http.ResponseWriter, r *http.Request) {
	g, ok := s.loadGroup(w, r)
	if !ok {
		return
	}
	back := "/admin/groups/" + g.ID.String()
	userID, ok := formID(r, "user_id")
	if !ok {
		s.redirectFlash(w, r, back, "error", "Choose a user.")
		return
	}
	ctx := r.Context()
	switch r.FormValue("action") {
	case "add":
		err := s.store.AddGroupMember(ctx, g.ID, userID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			s.redirectFlash(w, r, back, "error", "User not found.")
			return
		case errors.Is(err, store.ErrAdminGroupMember):
			s.redirectFlash(w, r, back, "error", "Platform Admins cannot join groups.")
			return
		case err != nil:
			s.serverError(w, r, "add member", err)
			return
		}
	case "remove":
		if err := s.store.RemoveGroupMember(ctx, g.ID, userID); err != nil {
			s.serverError(w, r, "remove member", err)
			return
		}
		if err := s.store.RevokeUserSessions(ctx, userID); err != nil {
			s.serverError(w, r, "revoke sessions", err)
			return
		}
		s.redirectFlash(w, r, back, "success", "Members updated.")
		return
	default:
		s.renderError(w, r, http.StatusBadRequest, "Unknown action.")
		return
	}
	if err := s.store.MarkUserSessionsForRotation(ctx, userID); err != nil {
		s.serverError(w, r, "rotate sessions", err)
		return
	}
	s.redirectFlash(w, r, back, "success", "Members updated.")
}

func (s *Server) handleAdminGroupAccess(w http.ResponseWriter, r *http.Request) {
	g, ok := s.loadGroup(w, r)
	if !ok {
		return
	}
	back := "/admin/groups/" + g.ID.String()
	dbID, ok := formID(r, "db_id")
	if !ok {
		s.redirectFlash(w, r, back, "error", "Choose a database.")
		return
	}
	level := domain.ParseLevel(r.FormValue("level"))
	ctx := r.Context()
	if level != domain.LevelNone {
		if _, err := s.store.GetDBConnection(ctx, dbID); errors.Is(err, store.ErrNotFound) {
			s.redirectFlash(w, r, back, "error", "Database not found.")
			return
		} else if err != nil {
			s.serverError(w, r, "get database", err)
			return
		}
	}
	previous, err := s.groupLevel(ctx, g.ID, dbID)
	if err != nil {
		s.serverError(w, r, "get group access", err)
		return
	}
	if err := s.store.SetGroupAccess(ctx, g.ID, dbID, level); err != nil {
		s.serverError(w, r, "set group access", err)
		return
	}
	if level > previous {
		err = s.store.MarkGroupMembersSessionsForRotation(ctx, g.ID)
	} else if level < previous {
		err = s.store.RevokeGroupMembersSessions(ctx, g.ID)
	}
	if err != nil {
		s.serverError(w, r, "update member sessions", err)
		return
	}
	msg := "Access updated."
	if level == domain.LevelNone {
		msg = "Access revoked."
	}
	s.redirectFlash(w, r, back, "success", msg)
}

func (s *Server) groupLevel(ctx context.Context, groupID, dbID uuid.UUID) (domain.AccessLevel, error) {
	grants, err := s.store.ListGroupAccess(ctx, groupID)
	if err != nil {
		return domain.LevelNone, err
	}
	for _, ga := range grants {
		if ga.DBID == dbID {
			return ga.Level, nil
		}
	}
	return domain.LevelNone, nil
}

func (s *Server) handleAdminGroupDelete(w http.ResponseWriter, r *http.Request) {
	g, ok := s.loadGroup(w, r)
	if !ok {
		return
	}
	members, err := s.store.DeleteGroup(r.Context(), g.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, r, "delete group", err)
		return
	}
	for _, id := range members {
		if err := s.store.RevokeUserSessions(r.Context(), id); err != nil {
			s.serverError(w, r, "revoke sessions", err)
			return
		}
	}
	s.redirectFlash(w, r, "/admin/groups", "success", "Group "+g.Name+" deleted.")
}

// Databases

func toDBView(c domain.DBConnection) dbView {
	return dbView{ID: c.ID, Name: c.Name, Host: c.Host, Port: c.Port, DBName: c.DBName, Username: c.Username,
		SSLMode: c.SSLMode, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

func (s *Server) handleAdminDatabases(w http.ResponseWriter, r *http.Request) {
	conns, err := s.store.ListDBConnections(r.Context())
	if err != nil {
		s.serverError(w, r, "list databases", err)
		return
	}
	var v adminDatabasesView
	for _, c := range conns {
		v.Databases = append(v.Databases, toDBView(c))
	}
	s.render(w, r, http.StatusOK, "admin/databases.html", pageData{Title: "Connections", Nav: navAdminDatabases, Data: v})
}

func (s *Server) handleAdminDatabaseNew(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "admin/database_form.html", pageData{Title: "Add connection", Nav: navAdminDatabases, Data: adminDatabaseFormView{}})
}

// Password kosong saat edit berarti tetap.
func connForm(r *http.Request, editing bool) (domain.DBConnection, string, formState) {
	v := map[string]string{}
	for _, k := range []string{"name", "host", "port", "dbname", "username", "sslmode"} {
		v[k] = strings.TrimSpace(r.FormValue(k))
	}
	password := r.FormValue("password")
	errs := map[string]string{}
	required := map[string]string{"name": "Display name", "host": "Host", "dbname": "Database name", "username": "Database user"}
	for k, label := range required {
		if v[k] == "" {
			errs[k] = label + " is required."
		} else if utf8.RuneCountInString(v[k]) > 255 {
			errs[k] = label + " is too long."
		}
	}
	if errs["name"] == "" && utf8.RuneCountInString(v["name"]) > maxNameLen {
		errs["name"] = "Display name must be at most 100 characters."
	}
	port, err := strconv.Atoi(v["port"])
	if err != nil || port < 1 || port > 65535 {
		errs["port"] = "Port must be between 1 and 65535."
	}
	if !sslModes[v["sslmode"]] {
		errs["sslmode"] = "Choose a valid SSL mode."
		errs["form"] = errs["sslmode"]
	}
	if password == "" && !editing {
		errs["password"] = "Password is required."
	} else if len(password) > 1024 {
		errs["password"] = "Password is too long."
	}
	conn := domain.DBConnection{Name: v["name"], Host: v["host"], Port: port, DBName: v["dbname"], Username: v["username"], SSLMode: v["sslmode"]}
	return conn, password, formState{Values: v, Errors: errs}
}

func (s *Server) renderDatabaseForm(w http.ResponseWriter, r *http.Request, status int, editing *domain.DBConnection, form formState) {
	v := adminDatabaseFormView{Form: form}
	title := "Add connection"
	if editing != nil {
		ev := toDBView(*editing)
		v.Editing = &ev
		title = editing.Name
	}
	s.render(w, r, status, "admin/database_form.html", pageData{Title: title, Nav: navAdminDatabases, Data: v})
}

func (s *Server) handleAdminDatabaseCreate(w http.ResponseWriter, r *http.Request) {
	conn, password, form := connForm(r, false)
	if len(form.Errors) > 0 {
		s.renderDatabaseForm(w, r, http.StatusUnprocessableEntity, nil, form)
		return
	}
	enc, err := s.box.Encrypt(password)
	if err != nil {
		s.serverError(w, r, "encrypt password", err)
		return
	}
	conn.PasswordEnc = enc
	actor := currentUser(r).ID
	conn.CreatedBy = &actor
	created, err := s.store.CreateDBConnection(r.Context(), conn)
	if errors.Is(err, store.ErrConflict) {
		form.Errors["name"] = "A connection with this name already exists."
		s.renderDatabaseForm(w, r, http.StatusUnprocessableEntity, nil, form)
		return
	}
	if err != nil {
		s.serverError(w, r, "create database", err)
		return
	}
	s.redirectFlash(w, r, "/admin/databases/"+created.ID.String(), "success", "Connection "+created.Name+" added. Test it to make sure it works.")
}

func (s *Server) loadConnection(w http.ResponseWriter, r *http.Request) (domain.DBConnection, bool) {
	id, ok := urlID(r, "dbID")
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "Connection not found.")
		return domain.DBConnection{}, false
	}
	c, err := s.store.GetDBConnection(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "Connection not found.")
		return domain.DBConnection{}, false
	}
	if err != nil {
		s.serverError(w, r, "get database", err)
		return domain.DBConnection{}, false
	}
	return c, true
}

func (s *Server) handleAdminDatabase(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadConnection(w, r)
	if !ok {
		return
	}
	s.renderDatabaseForm(w, r, http.StatusOK, &c, formState{Values: connFormValues(toDBView(c))})
}

func (s *Server) handleAdminDatabaseUpdate(w http.ResponseWriter, r *http.Request) {
	existing, ok := s.loadConnection(w, r)
	if !ok {
		return
	}
	conn, password, form := connForm(r, true)
	if len(form.Errors) > 0 {
		s.renderDatabaseForm(w, r, http.StatusUnprocessableEntity, &existing, form)
		return
	}
	conn.ID = existing.ID
	conn.PasswordEnc = existing.PasswordEnc
	if password != "" {
		enc, err := s.box.Encrypt(password)
		if err != nil {
			s.serverError(w, r, "encrypt password", err)
			return
		}
		conn.PasswordEnc = enc
	}
	updated, err := s.store.UpdateDBConnection(r.Context(), conn)
	if errors.Is(err, store.ErrConflict) {
		form.Errors["name"] = "A connection with this name already exists."
		s.renderDatabaseForm(w, r, http.StatusUnprocessableEntity, &existing, form)
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "Connection not found.")
		return
	}
	if err != nil {
		s.serverError(w, r, "update database", err)
		return
	}
	s.targets.Invalidate(updated.ID)
	s.redirectFlash(w, r, "/admin/databases/"+updated.ID.String(), "success", "Connection "+updated.Name+" saved.")
}

// Kembali ke halaman asal (daftar atau form edit).
func adminDatabasesBack(r *http.Request, id uuid.UUID) string {
	if ref, err := url.Parse(r.Referer()); err == nil && ref.Path == "/admin/databases/"+id.String() {
		return ref.Path
	}
	return "/admin/databases"
}

func (s *Server) handleAdminDatabaseTest(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadConnection(w, r)
	if !ok {
		return
	}
	back := adminDatabasesBack(r, c.ID)
	if err := s.targets.Test(r.Context(), c); err != nil {
		s.redirectFlash(w, r, back, "error", "Connection test for "+c.Name+" failed: "+err.Error())
		return
	}
	s.redirectFlash(w, r, back, "success", "Connection test for "+c.Name+" succeeded.")
}

func (s *Server) handleAdminDatabaseDelete(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadConnection(w, r)
	if !ok {
		return
	}
	err := s.store.DeleteDBConnection(r.Context(), c.ID)
	switch {
	case errors.Is(err, store.ErrRunningMigration):
		s.redirectFlash(w, r, "/admin/databases/"+c.ID.String(), "error", "A migration is running on "+c.Name+". Try again after it finishes.")
		return
	case err != nil && !errors.Is(err, store.ErrNotFound):
		s.serverError(w, r, "delete database", err)
		return
	}
	s.targets.Invalidate(c.ID)
	s.redirectFlash(w, r, "/admin/databases", "success", "Connection "+c.Name+" deleted.")
}
