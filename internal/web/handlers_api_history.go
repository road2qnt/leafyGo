package web

import (
	"net/http"
	"strings"

	"leafygo/internal/domain"
)

func (s *Server) handleAPIHistoryList(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	db := currentDB(r)
	level := currentLevel(r)

	hideRollback := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("hide_rollback")), "true") ||
		strings.TrimSpace(r.URL.Query().Get("hide_rollback")) == "1"

	execs, err := s.migrations.ListHistory(r.Context(), *user, level, db.ID, hideRollback)
	if err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}
	if execs == nil {
		execs = []domain.MigrationExecution{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"hide_rollback": hideRollback,
		"executions":    execs,
	})
}

func (s *Server) handleAPIHistoryDetail(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	db := currentDB(r)
	level := currentLevel(r)
	execID, ok := urlID(r, "execID")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Execution record not found.")
		return
	}

	exec, err := s.migrations.GetHistoryExecution(r.Context(), *user, level, db.ID, execID)
	if err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, exec)
}

