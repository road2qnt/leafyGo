package web

import (
	"errors"
	"net/http"
	"strings"

	"leafygo/internal/domain"
	"leafygo/internal/introspect"
	"leafygo/internal/queryconsole"
	"leafygo/internal/store"
)

type apiAccessibleDatabase struct {
	apiAdminDatabase
	AccessLevel string         `json:"access_level"`
	LockState   string         `json:"lock_state,omitempty"`
	Lock        *domain.DBLock `json:"lock,omitempty"`
}

func (s *Server) handleAPIDatabasesList(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		writeAPIError(w, r, http.StatusUnauthorized, ErrCodeUnauthorized, "Authentication required.")
		return
	}
	if user.IsPlatformAdmin {
		s.auditDenied(r, "list_databases", "databases")
		writeAPIError(w, r, http.StatusForbidden, ErrCodeForbidden, "Platform Admin accounts cannot access database tools.")
		return
	}
	dbs, err := s.store.ListAccessibleDBs(r.Context(), user.ID)
	if err != nil {
		s.logger.Error("api list accessible dbs", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to list accessible databases.")
		return
	}
	out := make([]apiAccessibleDatabase, 0, len(dbs))
	for _, d := range dbs {
		item := apiAccessibleDatabase{
			apiAdminDatabase: toAPIAdminDatabase(d.DBConnection),
			AccessLevel:      d.Level.String(),
		}
		if lock, err := s.store.GetDatabaseLock(r.Context(), d.ID); err == nil && lock != nil {
			item.LockState = lock.LockState
			item.Lock = lock
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"databases": out})
}

func (s *Server) handleAPIDatabaseOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	db := currentDB(r)
	level := currentLevel(r)

	targetReachable := true
	targetErrText := ""
	tableCount := 0
	if tables, err := s.listTables(r); err != nil {
		s.logTargetError(r, err)
		targetReachable = false
		targetErrText = targetErrorText(err)
	} else {
		tableCount = len(tables)
	}

	lock, err := s.store.GetDatabaseLock(ctx, db.ID)
	if err != nil {
		s.logger.Error("api get db lock", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to load database lock status.")
		return
	}
	var lockState string
	if lock != nil {
		lockState = lock.LockState
	}

	activeStack, err := s.store.GetActiveMigrationStack(ctx, db.ID)
	if err != nil {
		s.logger.Error("api get active migration stack", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to load active migration stack.")
		return
	}
	if activeStack == nil {
		activeStack = []store.ActiveStackEntry{}
	}

	approverGroups, err := s.store.ListRequiredApproverGroups(ctx, db.ID)
	if err != nil {
		s.logger.Error("api list required approver groups", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Failed to load required approver groups.")
		return
	}
	type apiApproverGroup struct {
		GroupID        string `json:"group_id"`
		GroupName      string `json:"group_name"`
		LeaderID       string `json:"leader_id,omitempty"`
		LeaderUsername string `json:"leader_username"`
		LeaderActive   bool   `json:"leader_active"`
		AccessLevel    string `json:"access_level"`
	}
	approversOut := make([]apiApproverGroup, 0, len(approverGroups))
	for _, ag := range approverGroups {
		lid := ""
		if ag.LeaderID != nil {
			lid = ag.LeaderID.String()
		}
		approversOut = append(approversOut, apiApproverGroup{
			GroupID:        ag.GroupID.String(),
			GroupName:      ag.GroupName,
			LeaderID:       lid,
			LeaderUsername: ag.LeaderUsername,
			LeaderActive:   ag.LeaderActive,
			AccessLevel:    ag.AccessLevel.String(),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"database":                 toAPIAdminDatabase(*db),
		"access_level":             level.String(),
		"target_reachable":         targetReachable,
		"target_error":             targetErrText,
		"table_count":              tableCount,
		"lock_state":               lockState,
		"lock":                     lock,
		"active_migration_stack":   activeStack,
		"required_approver_groups": approversOut,
	})
}

func (s *Server) handleAPIDatabaseSchema(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	schemaName := strings.TrimSpace(q.Get("schema"))
	tableName := strings.TrimSpace(q.Get("table"))
	if tableName != "" && schemaName == "" {
		if strings.Contains(tableName, ".") {
			parts := strings.SplitN(tableName, ".", 2)
			schemaName, tableName = parts[0], parts[1]
		} else {
			schemaName = "public"
		}
	}

	pool, err := s.targetPool(r)
	if err != nil {
		s.logTargetError(r, err)
		writeAPIError(w, r, http.StatusBadGateway, ErrCodeValidation, targetErrorText(err))
		return
	}

	tables, err := introspect.ListTables(r.Context(), pool)
	if err != nil {
		s.logTargetError(r, err)
		writeAPIError(w, r, http.StatusBadGateway, ErrCodeValidation, targetErrorText(err))
		return
	}

	if tableName == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"tables": tables,
		})
		return
	}

	detail, err := introspect.Describe(r.Context(), pool, schemaName, tableName)
	if errors.Is(err, introspect.ErrNotFound) {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Table "+schemaName+"."+tableName+" not found.")
		return
	}
	if err != nil {
		s.logTargetError(r, err)
		writeAPIError(w, r, http.StatusBadGateway, ErrCodeValidation, targetErrorText(err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tables": tables,
		"detail": detail,
	})
}

func (s *Server) handleAPIDatabaseData(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	schemaName := strings.TrimSpace(q.Get("schema"))
	tableName := strings.TrimSpace(q.Get("table"))
	if tableName == "" {
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, "Query parameter 'table' is required.")
		return
	}
	if schemaName == "" {
		if strings.Contains(tableName, ".") {
			parts := strings.SplitN(tableName, ".", 2)
			schemaName, tableName = parts[0], parts[1]
		} else {
			schemaName = "public"
		}
	}

	pool, err := s.targetPool(r)
	if err != nil {
		s.logTargetError(r, err)
		writeAPIError(w, r, http.StatusBadGateway, ErrCodeValidation, targetErrorText(err))
		return
	}

	d, err := introspect.Describe(r.Context(), pool, schemaName, tableName)
	if errors.Is(err, introspect.ErrNotFound) {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Table "+schemaName+"."+tableName+" not found.")
		return
	}
	if err != nil {
		s.logTargetError(r, err)
		writeAPIError(w, r, http.StatusBadGateway, ErrCodeValidation, targetErrorText(err))
		return
	}
	if !d.CanSelect {
		writeAPIError(w, r, http.StatusForbidden, ErrCodeForbidden, "No SELECT privilege on "+schemaName+"."+tableName+".")
		return
	}

	page := queryInt(r, "page", 1)
	size := min(queryInt(r, "limit", s.cfg.ViewerPageSize), s.cfg.ViewerMaxPageSize)
	p, err := introspect.ReadPage(r.Context(), pool, d, page, size)
	if err != nil {
		s.logTargetError(r, err)
		writeAPIError(w, r, http.StatusBadGateway, ErrCodeValidation, targetErrorText(err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"schema":    schemaName,
		"table":     tableName,
		"page":      page,
		"page_size": size,
		"has_next":  p.HasNext,
		"ordered":   p.Ordered,
		"row_count": d.RowCount,
		"columns":   p.Columns,
		"rows":      p.Rows,
	})
}

func (s *Server) handleAPIDatabaseQuery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SQL string `json:"sql"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	sql := strings.ReplaceAll(req.SQL, "\r\n", "\n")
	user := currentUser(r)
	db := currentDB(r)
	level := currentLevel(r)

	res, err := s.queries.Execute(r.Context(), queryconsole.ExecuteInput{
		Actor: *user,
		DB:    *db,
		Level: level,
		SQL:   sql,
	})
	if err != nil {
		var tgtErr *queryconsole.TargetError
		switch {
		case errors.Is(err, queryconsole.ErrForbidden):
			writeAPIError(w, r, http.StatusForbidden, ErrCodeForbidden, "You do not have access to run queries on this database.")
		case errors.Is(err, queryconsole.ErrBusy):
			writeAPIError(w, r, http.StatusConflict, ErrCodeConflict, "All query execution slots are currently in use.")
		case errors.Is(err, queryconsole.ErrTimeout):
			writeAPIError(w, r, http.StatusGatewayTimeout, ErrCodeValidation, "Query execution timed out.")
		case errors.As(err, &tgtErr):
			if tgtErr.Code == "55P03" {
				writeAPIError(w, r, http.StatusConflict, ErrCodeConflict, tgtErr.Message)
			} else if tgtErr.Code == "57014" {
				writeAPIError(w, r, http.StatusGatewayTimeout, ErrCodeValidation, "Query execution timed out.")
			} else {
				writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, tgtErr.Message)
			}
		default:
			writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, res)
}

