package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"leafygo/internal/auth"
	"leafygo/internal/domain"
	"leafygo/internal/store"
)

type apiGroupOption struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type apiAdminUser struct {
	ID              uuid.UUID        `json:"id"`
	Username        string           `json:"username"`
	IsPlatformAdmin bool             `json:"is_platform_admin"`
	IsAdmin         bool             `json:"is_admin"`
	IsActive        bool             `json:"is_active"`
	Groups          []apiGroupOption `json:"groups"`
	CreatedAt       time.Time        `json:"created_at"`
}

type apiUserOption struct {
	ID       uuid.UUID `json:"id"`
	Username string    `json:"username"`
	IsActive bool      `json:"is_active"`
}

type apiGroupAccessItem struct {
	DBID   uuid.UUID `json:"db_id"`
	DBName string    `json:"db_name"`
	Level  string    `json:"level"`
}

type apiAdminGroup struct {
	ID             uuid.UUID            `json:"id"`
	Name           string               `json:"name"`
	Description    string               `json:"description"`
	LeaderID       *uuid.UUID           `json:"leader_id,omitempty"`
	LeaderUsername string               `json:"leader_username"`
	Members        []apiUserOption      `json:"members"`
	Access         []apiGroupAccessItem `json:"access"`
	CreatedAt      time.Time            `json:"created_at"`
}

type apiAdminDatabase struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	Host      string     `json:"host"`
	Port      int        `json:"port"`
	DBName    string     `json:"dbname"`
	Username  string     `json:"username"`
	SSLMode   string     `json:"sslmode"`
	CreatedBy *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func toAPIAdminDatabase(c domain.DBConnection) apiAdminDatabase {
	return apiAdminDatabase{
		ID:        c.ID,
		Name:      c.Name,
		Host:      c.Host,
		Port:      c.Port,
		DBName:    c.DBName,
		Username:  c.Username,
		SSLMode:   c.SSLMode,
		CreatedBy: c.CreatedBy,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	}
}

func (s *Server) handleAPIAdminUsersList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		s.logger.Error("api list users", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to list users.")
		return
	}
	groups, err := s.store.ListGroups(ctx)
	if err != nil {
		s.logger.Error("api list groups for users", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to list groups.")
		return
	}
	memberOf := make(map[uuid.UUID][]apiGroupOption)
	for _, g := range groups {
		members, err := s.store.ListGroupMembers(ctx, g.ID)
		if err != nil {
			s.logger.Error("api list group members", "error", err)
			writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to list group members.")
			return
		}
		for _, m := range members {
			memberOf[m.ID] = append(memberOf[m.ID], apiGroupOption{ID: g.ID, Name: g.Name})
		}
	}
	activeAdmins, err := s.store.CountActivePlatformAdmins(ctx)
	if err != nil {
		s.logger.Error("api count active admins", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to count active admins.")
		return
	}

	out := make([]apiAdminUser, 0, len(users))
	for _, u := range users {
		grps := memberOf[u.ID]
		if grps == nil {
			grps = []apiGroupOption{}
		}
		out = append(out, apiAdminUser{
			ID:              u.ID,
			Username:        u.Username,
			IsPlatformAdmin: u.IsPlatformAdmin,
			IsAdmin:         u.IsPlatformAdmin,
			IsActive:        u.IsActive,
			Groups:          grps,
			CreatedAt:       u.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"users":         out,
		"active_admins": activeAdmins,
	})
}

func (s *Server) handleAPIAdminUserCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username        string `json:"username"`
		Password        string `json:"password"`
		IsAdmin         bool   `json:"is_admin"`
		IsPlatformAdmin bool   `json:"is_platform_admin"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	username := strings.TrimSpace(req.Username)
	if !usernamePattern.MatchString(username) {
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, "Use 3 to 64 letters, digits, dots, underscores, or hyphens.")
		return
	}
	if msg := validatePassword(req.Password); msg != "" {
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, msg)
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.logger.Error("api hash password", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to hash password.")
		return
	}
	isAdmin := req.IsAdmin || req.IsPlatformAdmin
	u, err := s.store.CreateUser(r.Context(), username, hash, isAdmin)
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, r, http.StatusConflict, ErrCodeConflict, "This username is already taken.")
		return
	}
	if err != nil {
		s.logger.Error("api create user", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to create user.")
		return
	}
	writeJSON(w, http.StatusCreated, apiAdminUser{
		ID:              u.ID,
		Username:        u.Username,
		IsPlatformAdmin: u.IsPlatformAdmin,
		IsAdmin:         u.IsPlatformAdmin,
		IsActive:        u.IsActive,
		Groups:          []apiGroupOption{},
		CreatedAt:       u.CreatedAt,
	})
}

func (s *Server) handleAPIAdminUserActive(w http.ResponseWriter, r *http.Request) {
	id, ok := urlID(r, "userID")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "User not found.")
		return
	}
	var req struct {
		Active   *bool `json:"active"`
		IsActive *bool `json:"is_active"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	var active bool
	switch {
	case req.Active != nil:
		active = *req.Active
	case req.IsActive != nil:
		active = *req.IsActive
	default:
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, "Field 'active' or 'is_active' is required.")
		return
	}

	err := s.store.SetUserActiveGuarded(r.Context(), id, active, currentUser(r).ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "User not found.")
		return
	case errors.Is(err, store.ErrSelfMutation):
		writeAPIError(w, r, http.StatusConflict, ErrCodeConflict, "You cannot deactivate your own account.")
		return
	case errors.Is(err, store.ErrLastAdmin):
		writeAPIError(w, r, http.StatusConflict, ErrCodeConflict, "The last active admin cannot be deactivated.")
		return
	case err != nil:
		s.logger.Error("api set user active", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to update user active state.")
		return
	}
	if !active {
		_ = s.store.RevokeUserSessions(r.Context(), id)
	}
	u, err := s.store.GetUserByID(r.Context(), id)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to reload user.")
		return
	}
	writeJSON(w, http.StatusOK, apiAdminUser{
		ID:              u.ID,
		Username:        u.Username,
		IsPlatformAdmin: u.IsPlatformAdmin,
		IsAdmin:         u.IsPlatformAdmin,
		IsActive:        u.IsActive,
		Groups:          []apiGroupOption{},
		CreatedAt:       u.CreatedAt,
	})
}

func (s *Server) handleAPIAdminUserPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := urlID(r, "userID")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "User not found.")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if msg := validatePassword(req.Password); msg != "" {
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, msg)
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.logger.Error("api hash password", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to hash password.")
		return
	}
	if err := s.store.UpdateUserPassword(r.Context(), id, hash); errors.Is(err, store.ErrNotFound) {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "User not found.")
		return
	} else if err != nil {
		s.logger.Error("api update password", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to update password.")
		return
	}
	_ = s.store.RevokeUserSessions(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "password_updated"})
}

func (s *Server) handleAPIAdminUserDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := urlID(r, "userID")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "User not found.")
		return
	}
	err := s.store.DeleteUserGuarded(r.Context(), id, currentUser(r).ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "User not found.")
		return
	case errors.Is(err, store.ErrSelfMutation):
		writeAPIError(w, r, http.StatusConflict, ErrCodeConflict, "You cannot delete your own account.")
		return
	case errors.Is(err, store.ErrLastAdmin):
		writeAPIError(w, r, http.StatusConflict, ErrCodeConflict, "The last active admin cannot be deleted.")
		return
	case errors.Is(err, store.ErrUserHasHistory):
		writeAPIError(w, r, http.StatusConflict, ErrCodeUserHasHistory, "User has migration proposal or approval history and cannot be hard-deleted. Deactivate the account instead.")
		return
	case err != nil:
		s.logger.Error("api delete user", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to delete user.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) buildAPIAdminGroup(ctx context.Context, g domain.Group) (apiAdminGroup, error) {
	out := apiAdminGroup{
		ID:             g.ID,
		Name:           g.Name,
		Description:    g.Description,
		LeaderID:       g.LeaderID,
		LeaderUsername: g.LeaderUsername,
		Members:        []apiUserOption{},
		Access:         []apiGroupAccessItem{},
		CreatedAt:      g.CreatedAt,
	}
	members, err := s.store.ListGroupMembers(ctx, g.ID)
	if err != nil {
		return out, err
	}
	for _, m := range members {
		out.Members = append(out.Members, apiUserOption{ID: m.ID, Username: m.Username, IsActive: m.IsActive})
	}
	accessGrants, err := s.store.ListGroupAccess(ctx, g.ID)
	if err != nil {
		return out, err
	}
	for _, a := range accessGrants {
		out.Access = append(out.Access, apiGroupAccessItem{DBID: a.DBID, DBName: a.DBName, Level: a.Level.String()})
	}
	return out, nil
}

func (s *Server) handleAPIAdminGroupsList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	groups, err := s.store.ListGroups(ctx)
	if err != nil {
		s.logger.Error("api list groups", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to list groups.")
		return
	}
	out := make([]apiAdminGroup, 0, len(groups))
	for _, g := range groups {
		item, err := s.buildAPIAdminGroup(ctx, g)
		if err != nil {
			s.logger.Error("api build group", "error", err)
			writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to load group details.")
			return
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}

func (s *Server) handleAPIAdminGroupGet(w http.ResponseWriter, r *http.Request) {
	id, ok := urlID(r, "groupID")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Group not found.")
		return
	}
	g, err := s.store.GetGroup(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Group not found.")
		return
	}
	if err != nil {
		s.logger.Error("api get group", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to load group.")
		return
	}
	item, err := s.buildAPIAdminGroup(r.Context(), g)
	if err != nil {
		s.logger.Error("api build group", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to load group details.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleAPIAdminGroupCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	desc := strings.TrimSpace(req.Description)
	if name == "" || utf8.RuneCountInString(name) > maxNameLen {
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, "Group name is required and must be at most 100 characters.")
		return
	}
	if utf8.RuneCountInString(desc) > maxDescLen {
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, "Description must be at most 500 characters.")
		return
	}
	g, err := s.store.CreateGroup(r.Context(), name, desc)
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, r, http.StatusConflict, ErrCodeConflict, "A group with this name already exists.")
		return
	}
	if err != nil {
		s.logger.Error("api create group", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to create group.")
		return
	}
	item, err := s.buildAPIAdminGroup(r.Context(), g)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to load group.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleAPIAdminGroupUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := urlID(r, "groupID")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Group not found.")
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	desc := strings.TrimSpace(req.Description)
	if name == "" || utf8.RuneCountInString(name) > maxNameLen {
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, "Group name is required and must be at most 100 characters.")
		return
	}
	if utf8.RuneCountInString(desc) > maxDescLen {
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, "Description must be at most 500 characters.")
		return
	}
	updated, err := s.store.UpdateGroup(r.Context(), id, name, desc)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Group not found.")
		return
	case errors.Is(err, store.ErrConflict):
		writeAPIError(w, r, http.StatusConflict, ErrCodeConflict, "A group with this name already exists.")
		return
	case err != nil:
		s.logger.Error("api update group", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to update group.")
		return
	}
	item, err := s.buildAPIAdminGroup(r.Context(), updated)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to load group.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleAPIAdminGroupLeader(w http.ResponseWriter, r *http.Request) {
	groupID, ok := urlID(r, "groupID")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Group not found.")
		return
	}
	var req struct {
		LeaderID *uuid.UUID `json:"leader_id"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	updated, err := s.store.SetGroupLeader(r.Context(), groupID, req.LeaderID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Group not found.")
		return
	case errors.Is(err, store.ErrInvalidGroupLeader):
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, "Leader must be an active member of this group.")
		return
	case err != nil:
		s.logger.Error("api set group leader", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to assign group leader.")
		return
	}
	item, err := s.buildAPIAdminGroup(r.Context(), updated)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to load group.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleAPIAdminGroupAddMember(w http.ResponseWriter, r *http.Request) {
	groupID, ok := urlID(r, "groupID")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Group not found.")
		return
	}
	if _, err := s.store.GetGroup(r.Context(), groupID); errors.Is(err, store.ErrNotFound) {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Group not found.")
		return
	} else if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to load group.")
		return
	}
	var req struct {
		UserID uuid.UUID `json:"user_id"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.UserID == uuid.Nil {
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, "user_id is required.")
		return
	}
	err := s.store.AddGroupMember(r.Context(), groupID, req.UserID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "User not found.")
		return
	case errors.Is(err, store.ErrAdminGroupMember):
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, "Platform Admins cannot join groups.")
		return
	case err != nil:
		s.logger.Error("api add group member", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to add group member.")
		return
	}
	_ = s.store.MarkUserSessionsForRotation(r.Context(), req.UserID)
	g, err := s.store.GetGroup(r.Context(), groupID)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to reload group.")
		return
	}
	item, err := s.buildAPIAdminGroup(r.Context(), g)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to reload group.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleAPIAdminGroupRemoveMember(w http.ResponseWriter, r *http.Request) {
	groupID, ok := urlID(r, "groupID")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Group not found.")
		return
	}
	userID, ok := urlID(r, "userID")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "User not found.")
		return
	}
	if _, err := s.store.GetGroup(r.Context(), groupID); errors.Is(err, store.ErrNotFound) {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Group not found.")
		return
	} else if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to load group.")
		return
	}
	if err := s.store.RemoveGroupMember(r.Context(), groupID, userID); err != nil {
		s.logger.Error("api remove group member", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to remove group member.")
		return
	}
	_ = s.store.RevokeUserSessions(r.Context(), userID)
	g, err := s.store.GetGroup(r.Context(), groupID)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to reload group.")
		return
	}
	item, err := s.buildAPIAdminGroup(r.Context(), g)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to reload group.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleAPIAdminGroupAccess(w http.ResponseWriter, r *http.Request) {
	groupID, ok := urlID(r, "groupID")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Group not found.")
		return
	}
	if _, err := s.store.GetGroup(r.Context(), groupID); errors.Is(err, store.ErrNotFound) {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Group not found.")
		return
	} else if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to load group.")
		return
	}
	var req struct {
		DBID  uuid.UUID `json:"db_id"`
		Level string    `json:"level"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.DBID == uuid.Nil {
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, "db_id is required.")
		return
	}
	level := domain.ParseLevel(req.Level)
	ctx := r.Context()
	if level != domain.LevelNone {
		if _, err := s.store.GetDBConnection(ctx, req.DBID); errors.Is(err, store.ErrNotFound) {
			writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Database not found.")
			return
		} else if err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to load database.")
			return
		}
	}
	previous, err := s.groupLevel(ctx, groupID, req.DBID)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to check group access.")
		return
	}
	if err := s.store.SetGroupAccess(ctx, groupID, req.DBID, level); err != nil {
		s.logger.Error("api set group access", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to update group access.")
		return
	}
	if level > previous {
		_ = s.store.MarkGroupMembersSessionsForRotation(ctx, groupID)
	} else if level < previous {
		_ = s.store.RevokeGroupMembersSessions(ctx, groupID)
	}
	g, err := s.store.GetGroup(ctx, groupID)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to reload group.")
		return
	}
	item, err := s.buildAPIAdminGroup(ctx, g)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to reload group.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleAPIAdminGroupDelete(w http.ResponseWriter, r *http.Request) {
	groupID, ok := urlID(r, "groupID")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Group not found.")
		return
	}
	soft := r.URL.Query().Get("soft") == "true"
	var members []uuid.UUID
	var err error
	if soft {
		members, err = s.store.SoftDeleteGroup(r.Context(), groupID)
	} else {
		members, err = s.store.DeleteGroup(r.Context(), groupID)
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Group not found.")
		return
	case errors.Is(err, store.ErrGroupHasHistory):
		writeAPIError(w, r, http.StatusConflict, ErrCodeGroupHasHistory, "Group has migration proposal or approval history and cannot be hard-deleted.")
		return
	case err != nil:
		s.logger.Error("api delete group", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to delete group.")
		return
	}
	for _, uid := range members {
		_ = s.store.RevokeUserSessions(r.Context(), uid)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleAPIAdminDatabasesList(w http.ResponseWriter, r *http.Request) {
	conns, err := s.store.ListDBConnections(r.Context())
	if err != nil {
		s.logger.Error("api list databases", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to list databases.")
		return
	}
	out := make([]apiAdminDatabase, 0, len(conns))
	for _, c := range conns {
		out = append(out, toAPIAdminDatabase(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"databases": out})
}

func (s *Server) handleAPIAdminDatabaseGet(w http.ResponseWriter, r *http.Request) {
	id, ok := urlID(r, "dbID")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Database not found.")
		return
	}
	c, err := s.store.GetDBConnection(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Database not found.")
		return
	}
	if err != nil {
		s.logger.Error("api get database", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to load database.")
		return
	}
	writeJSON(w, http.StatusOK, toAPIAdminDatabase(c))
}

type apiDatabaseUpsertRequest struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	DBName   string `json:"dbname"`
	Username string `json:"username"`
	Password string `json:"password"`
	SSLMode  string `json:"sslmode"`
}

func validateAPIDatabaseInput(req apiDatabaseUpsertRequest, editing bool) (domain.DBConnection, string, string) {
	name := strings.TrimSpace(req.Name)
	host := strings.TrimSpace(req.Host)
	dbname := strings.TrimSpace(req.DBName)
	username := strings.TrimSpace(req.Username)
	sslmode := strings.TrimSpace(req.SSLMode)
	if sslmode == "" {
		sslmode = "disable"
	}
	port := req.Port
	if port == 0 {
		port = 5432
	}
	if name == "" || utf8.RuneCountInString(name) > maxNameLen {
		return domain.DBConnection{}, "", "Display name is required and must be at most 100 characters."
	}
	if host == "" || utf8.RuneCountInString(host) > 255 {
		return domain.DBConnection{}, "", "Host is required."
	}
	if dbname == "" || utf8.RuneCountInString(dbname) > 255 {
		return domain.DBConnection{}, "", "Database name is required."
	}
	if username == "" || utf8.RuneCountInString(username) > 255 {
		return domain.DBConnection{}, "", "Database user is required."
	}
	if port < 1 || port > 65535 {
		return domain.DBConnection{}, "", "Port must be between 1 and 65535."
	}
	if !sslModes[sslmode] {
		return domain.DBConnection{}, "", "Choose a valid SSL mode."
	}
	if req.Password == "" && !editing {
		return domain.DBConnection{}, "", "Password is required."
	}
	if len(req.Password) > 1024 {
		return domain.DBConnection{}, "", "Password is too long."
	}
	return domain.DBConnection{
		Name:     name,
		Host:     host,
		Port:     port,
		DBName:   dbname,
		Username: username,
		SSLMode:  sslmode,
	}, req.Password, ""
}

func (s *Server) handleAPIAdminDatabaseCreate(w http.ResponseWriter, r *http.Request) {
	var req apiDatabaseUpsertRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	conn, password, errMsg := validateAPIDatabaseInput(req, false)
	if errMsg != "" {
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, errMsg)
		return
	}
	enc, err := s.box.Encrypt(password)
	if err != nil {
		s.logger.Error("api encrypt db password", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to encrypt credentials.")
		return
	}
	conn.PasswordEnc = enc
	actorID := currentUser(r).ID
	conn.CreatedBy = &actorID

	created, err := s.store.CreateDBConnection(r.Context(), conn)
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, r, http.StatusConflict, ErrCodeConflict, "A database connection with this name already exists.")
		return
	}
	if err != nil {
		s.logger.Error("api create database", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to create database connection.")
		return
	}
	writeJSON(w, http.StatusCreated, toAPIAdminDatabase(created))
}

func (s *Server) handleAPIAdminDatabaseUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := urlID(r, "dbID")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Database not found.")
		return
	}
	existing, err := s.store.GetDBConnection(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Database not found.")
		return
	}
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to load database.")
		return
	}

	var req apiDatabaseUpsertRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	conn, password, errMsg := validateAPIDatabaseInput(req, true)
	if errMsg != "" {
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, errMsg)
		return
	}
	conn.ID = existing.ID
	conn.PasswordEnc = existing.PasswordEnc
	if password != "" {
		enc, err := s.box.Encrypt(password)
		if err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to encrypt credentials.")
			return
		}
		conn.PasswordEnc = enc
	}

	updated, err := s.store.UpdateDBConnection(r.Context(), conn)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Database not found.")
		return
	case errors.Is(err, store.ErrConflict):
		writeAPIError(w, r, http.StatusConflict, ErrCodeConflict, "A database connection with this name already exists.")
		return
	case err != nil:
		s.logger.Error("api update database", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to update database connection.")
		return
	}
	s.targets.Invalidate(updated.ID)
	writeJSON(w, http.StatusOK, toAPIAdminDatabase(updated))
}

func (s *Server) handleAPIAdminDatabaseTest(w http.ResponseWriter, r *http.Request) {
	id, ok := urlID(r, "dbID")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Database not found.")
		return
	}
	c, err := s.store.GetDBConnection(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Database not found.")
		return
	}
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to load database.")
		return
	}
	if err := s.targets.Test(r.Context(), c); err != nil {
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, "Connection test failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "message": "Connection test succeeded."})
}

func (s *Server) handleAPIAdminDatabaseDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := urlID(r, "dbID")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Database not found.")
		return
	}
	err := s.store.DeleteDBConnection(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Database not found.")
		return
	case errors.Is(err, store.ErrDBHasActiveWork), errors.Is(err, store.ErrRunningMigration):
		writeAPIError(w, r, http.StatusConflict, ErrCodeDBHasActiveWork, "Database has an active migration lock or job in progress and cannot be deleted.")
		return
	case err != nil:
		s.logger.Error("api delete database", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to delete database connection.")
		return
	}
	s.targets.Invalidate(id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func init() {
	_ = chi.URLParam
}

