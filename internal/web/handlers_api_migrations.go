package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"leafygo/internal/domain"
	"leafygo/internal/migration"
	"leafygo/internal/store"
)

func (s *Server) writeMigrationWorkflowError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, migration.ErrForbidden):
		writeAPIError(w, r, http.StatusForbidden, ErrCodeForbidden, "You do not have permission to perform this action.")
	case errors.Is(err, migration.ErrNotGroupLeader), errors.Is(err, store.ErrNotGroupLeader):
		writeAPIError(w, r, http.StatusForbidden, ErrCodeForbiddenNotGroupLeader, "You are not an active Group Leader for any required approver group on this proposal.")
	case errors.Is(err, migration.ErrNotFound), errors.Is(err, store.ErrNotFound):
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Requested migration resource was not found.")
	case errors.Is(err, migration.ErrDryRunBusy):
		writeAPIError(w, r, http.StatusTooManyRequests, ErrCodeDryRunBusy, "A pre-review dry-run is already active on this database or user cooldown is in effect.")
	case errors.Is(err, migration.ErrNonTransactionalStmt):
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeNonTransactionalStmt, err.Error())
	case errors.Is(err, migration.ErrInvalidBatchIdentifier):
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeInvalidBatchIdentifier, err.Error())
	case errors.Is(err, migration.ErrInvalidBatchCursorType):
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeInvalidBatchCursorType, err.Error())
	case errors.Is(err, migration.ErrInvalidMigrationSQL):
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeInvalidMigrationSQL, err.Error())
	case errors.Is(err, migration.ErrNoAccessGroups), errors.Is(err, store.ErrNoAccessGroups):
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeNoAccessGroups, "No active groups have access to this database.")
	case errors.Is(err, migration.ErrMissingGroupLeader), errors.Is(err, store.ErrMissingGroupLeader):
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeMissingGroupLeader, err.Error())
	case errors.Is(err, migration.ErrDuplicateActiveProposal), errors.Is(err, store.ErrDuplicateActiveProposal), errors.Is(err, store.ErrActiveProposalExists):
		writeAPIError(w, r, http.StatusConflict, ErrCodeDuplicateActiveProposal, "An active proposal already exists for this migration definition.")
	case errors.Is(err, migration.ErrPredecessorNotReady), errors.Is(err, store.ErrPredecessorNotReady):
		writeAPIError(w, r, http.StatusConflict, ErrCodePredecessorNotReady, "Predecessor migration is not yet active or queued/running.")
	case errors.Is(err, migration.ErrMigrationNotSupersedable), errors.Is(err, store.ErrMigrationNotSupersedable):
		writeAPIError(w, r, http.StatusConflict, ErrCodeMigrationNotSupersedable, "Target migration cannot be superseded because it is currently active, in-flight, or not the lowest inactive effective_seq slot.")
	case errors.Is(err, migration.ErrMigrationImmutable), errors.Is(err, store.ErrMigrationImmutable):
		writeAPIError(w, r, http.StatusConflict, ErrCodeMigrationImmutable, "Migration definition is immutable and cannot be modified.")
	case errors.Is(err, migration.ErrConflict), errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrProposalNotPendingReview):
		writeAPIError(w, r, http.StatusConflict, ErrCodeConflict, err.Error())
	case errors.Is(err, migration.ErrValidation), errors.Is(err, store.ErrInvalidMigrationDef), errors.Is(err, store.ErrInvalidProposalAction):
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, err.Error())
	default:
		s.logger.Error("api migration workflow error", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Internal server error.")
	}
}

func (s *Server) handleAPIMigrationsList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	db := currentDB(r)

	defs, err := s.store.ListMigrationDefinitions(ctx, db.ID)
	if err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}
	if defs == nil {
		defs = []domain.MigrationDefinition{}
	}

	activeStack, err := s.store.GetActiveMigrationStack(ctx, db.ID)
	if err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}
	if activeStack == nil {
		activeStack = []store.ActiveStackEntry{}
	}

	topRollback, err := s.store.GetTopActiveMigrationForRollback(ctx, db.ID)
	if err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}

	nextForward, err := s.store.GetNextEligibleMigrationForForward(ctx, db.ID)
	if err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"migrations":          defs,
		"active_stack":        activeStack,
		"top_rollback_target": topRollback,
		"next_forward_target": nextForward,
	})
}

func (s *Server) handleAPIMigrationGet(w http.ResponseWriter, r *http.Request) {
	db := currentDB(r)
	mid, ok := urlID(r, "mid")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Migration definition not found.")
		return
	}
	def, err := s.store.GetMigrationDefinition(r.Context(), db.ID, mid)
	if err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, def)
}

type apiMigrationDefinitionUpsertRequest struct {
	Revision            int                 `json:"revision"`
	Title               string              `json:"title"`
	Description         string              `json:"description"`
	SQLUp               string              `json:"sql_up"`
	SQLDown             string              `json:"sql_down"`
	IsIrreversible      bool                `json:"is_irreversible"`
	IrreversibleReason  string              `json:"irreversible_reason"`
	ExecutionMode       string              `json:"execution_mode"`
	BatchConfig         *domain.BatchConfig `json:"batch_config"`
	ReplacesMigrationID *uuid.UUID          `json:"replaces_migration_id"`
}

func (s *Server) handleAPIMigrationCreate(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	db := currentDB(r)
	level := currentLevel(r)

	var req apiMigrationDefinitionUpsertRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	def := domain.MigrationDefinition{
		DBID:                db.ID,
		Title:               req.Title,
		Description:         req.Description,
		SQLUp:               req.SQLUp,
		SQLDown:             req.SQLDown,
		IsIrreversible:      req.IsIrreversible,
		IrreversibleReason:  req.IrreversibleReason,
		ExecutionMode:       req.ExecutionMode,
		BatchConfig:         req.BatchConfig,
		ReplacesMigrationID: req.ReplacesMigrationID,
	}

	created, err := s.migrations.CreateDefinition(r.Context(), *user, level, def)
	if err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleAPIMigrationUpdate(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	db := currentDB(r)
	level := currentLevel(r)
	mid, ok := urlID(r, "mid")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Migration definition not found.")
		return
	}

	var req apiMigrationDefinitionUpsertRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	def := domain.MigrationDefinition{
		ID:                  mid,
		DBID:                db.ID,
		Revision:            req.Revision,
		Title:               req.Title,
		Description:         req.Description,
		SQLUp:               req.SQLUp,
		SQLDown:             req.SQLDown,
		IsIrreversible:      req.IsIrreversible,
		IrreversibleReason:  req.IrreversibleReason,
		ExecutionMode:       req.ExecutionMode,
		BatchConfig:         req.BatchConfig,
		ReplacesMigrationID: req.ReplacesMigrationID,
	}

	updated, err := s.migrations.UpdateDefinition(r.Context(), *user, level, def)
	if err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleAPIMigrationDelete(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	db := currentDB(r)
	level := currentLevel(r)
	mid, ok := urlID(r, "mid")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Migration definition not found.")
		return
	}
	if err := s.migrations.DeleteDefinition(r.Context(), *user, level, db.ID, mid); err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAPIMigrationValidate(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	db := currentDB(r)
	level := currentLevel(r)
	mid, ok := urlID(r, "mid")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Migration definition not found.")
		return
	}
	def, err := s.store.GetMigrationDefinition(r.Context(), db.ID, mid)
	if err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}
	res, err := s.migrations.ValidateDefinition(r.Context(), *user, level, *db, def)
	if err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dry_run_status": res.Status,
		"message":        res.Warning,
		"sqlstate":       res.SQLState,
	})
}

func (s *Server) handleAPIProposalsList(w http.ResponseWriter, r *http.Request) {
	db := currentDB(r)
	proposals, err := s.store.ListMigrationProposals(r.Context(), db.ID)
	if err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}
	if proposals == nil {
		proposals = []domain.MigrationProposal{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"proposals": proposals})
}

func (s *Server) handleAPIProposalGet(w http.ResponseWriter, r *http.Request) {
	db := currentDB(r)
	pid, ok := urlID(r, "pid")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Proposal not found.")
		return
	}
	prop, err := s.store.GetMigrationProposal(r.Context(), db.ID, pid)
	if err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, prop)
}

type apiSubmitProposalRequest struct {
	MigrationID       uuid.UUID  `json:"migration_id"`
	ActionType        string     `json:"action_type"`
	TargetExecutionID *uuid.UUID `json:"target_execution_id"`
	Resolution        *string    `json:"resolution"`
	Reason            string     `json:"reason"`
}

func (s *Server) handleAPIProposalSubmit(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	db := currentDB(r)
	level := currentLevel(r)

	var req apiSubmitProposalRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.MigrationID == uuid.Nil {
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, "migration_id is required.")
		return
	}
	actionType := strings.TrimSpace(req.ActionType)
	if actionType == "" {
		actionType = domain.ActionApply
	}

	res, err := s.migrations.SubmitProposal(r.Context(), migration.SubmitProposalRequest{
		Actor:             *user,
		DB:                *db,
		Level:             level,
		MigrationID:       req.MigrationID,
		ActionType:        actionType,
		TargetExecutionID: req.TargetExecutionID,
		Resolution:        req.Resolution,
		Reason:            req.Reason,
	})
	if err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"proposal":        res.Proposal,
		"dry_run_status":  res.DryRun.Status,
		"dry_run_warning": res.DryRun.Warning,
		"dry_run":         res.DryRun,
	})
}

func (s *Server) handleAPIProposalWithdraw(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	db := currentDB(r)
	level := currentLevel(r)
	pid, ok := urlID(r, "pid")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Proposal not found.")
		return
	}

	prop, err := s.migrations.WithdrawProposal(r.Context(), *user, level, db.ID, pid)
	if err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   prop.Status,
		"proposal": prop,
	})
}

func (s *Server) handleAPIProposalDelete(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	db := currentDB(r)
	level := currentLevel(r)
	pid, ok := urlID(r, "pid")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Proposal not found.")
		return
	}
	if err := s.migrations.DeleteProposal(r.Context(), *user, level, db.ID, pid); err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type apiApprovalDecisionRequest struct {
	Comment string `json:"comment"`
}

func (s *Server) handleAPIProposalApprove(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	db := currentDB(r)
	pid, ok := urlID(r, "pid")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Proposal not found.")
		return
	}

	var req apiApprovalDecisionRequest
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeJSONBody(w, r, &req) {
			return
		}
	}

	res, err := s.migrations.ApproveProposal(r.Context(), store.ApproveProposalInput{
		DBID:          db.ID,
		ProposalID:    pid,
		Actor:         *user,
		Comment:       req.Comment,
		TraceParent:   strings.TrimSpace(r.Header.Get("traceparent")),
		TraceState:    strings.TrimSpace(r.Header.Get("tracestate")),
		CorrelationID: correlationIDFromRequest(r),
	})
	if err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}

	resp := map[string]any{
		"status":         res.Proposal.Status,
		"proposal":       res.Proposal,
		"required_count": res.RequiredCount,
		"approved_count": res.ApprovedCount,
		"enqueued":       res.Enqueued,
	}
	if res.Enqueued && res.Job != nil && res.Execution != nil {
		resp["job_id"] = res.Job.ID
		resp["execution_id"] = res.Execution.ExecutionID
		resp["root_execution_id"] = res.Execution.RootExecutionID
		resp["job"] = res.Job
		resp["execution"] = res.Execution
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAPIProposalReject(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	db := currentDB(r)
	pid, ok := urlID(r, "pid")
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Proposal not found.")
		return
	}

	var req apiApprovalDecisionRequest
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeJSONBody(w, r, &req) {
			return
		}
	}

	prop, err := s.migrations.RejectProposal(r.Context(), store.ApproveProposalInput{
		DBID:       db.ID,
		ProposalID: pid,
		Actor:      *user,
		Comment:    req.Comment,
	})
	if err != nil {
		s.writeMigrationWorkflowError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   prop.Status,
		"proposal": prop,
	})
}
