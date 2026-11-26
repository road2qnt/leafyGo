package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"leafygo/internal/domain"
	"leafygo/internal/store"
)

type jobStatusResponse struct {
	JobID            uuid.UUID  `json:"job_id"`
	DBID             uuid.UUID  `json:"db_id"`
	ProposalID       uuid.UUID  `json:"proposal_id"`
	MigrationID      uuid.UUID  `json:"migration_id"`
	ExecutionID      uuid.UUID  `json:"execution_id"`
	RootExecutionID  uuid.UUID  `json:"root_execution_id"`
	ActionType       string     `json:"action_type"`
	ExecutionMode    string     `json:"execution_mode"`
	Status           string     `json:"status"`
	RetryCount       int        `json:"retry_count"`
	MaxRetries       int        `json:"max_retries"`
	NextRunAt        time.Time  `json:"next_run_at"`
	TotalBatches     int        `json:"total_batches"`
	CompletedBatches int        `json:"completed_batches"`
	CommittedRows    int64      `json:"committed_rows"`
	LastCursor       *int64     `json:"last_cursor"`
	ScopeMinCursor   *int64     `json:"scope_min_cursor"`
	ScopeMaxCursor   *int64     `json:"scope_max_cursor"`
	LastError        string     `json:"last_error"`
	LockState        *string    `json:"lock_state"`
	QueuedAt         time.Time  `json:"queued_at"`
	StartedAt        *time.Time `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func newJobStatusResponse(j domain.MigrationJob, lock *domain.DBLock) jobStatusResponse {
	resp := jobStatusResponse{
		JobID:            j.ID,
		DBID:             j.DBID,
		ProposalID:       j.ProposalID,
		MigrationID:      j.MigrationID,
		ExecutionID:      j.ExecutionID,
		RootExecutionID:  j.RootExecutionID,
		ActionType:       j.ActionType,
		ExecutionMode:    j.ExecutionMode,
		Status:           j.Status,
		RetryCount:       j.RetryCount,
		MaxRetries:       j.MaxRetries,
		NextRunAt:        j.NextRunAt,
		TotalBatches:     j.TotalBatches,
		CompletedBatches: j.CompletedBatches,
		CommittedRows:    j.CommittedRows,
		LastCursor:       j.LastCursor,
		ScopeMinCursor:   j.ScopeMinCursor,
		ScopeMaxCursor:   j.ScopeMaxCursor,
		LastError:        j.LastError,
		QueuedAt:         j.QueuedAt,
		StartedAt:        j.StartedAt,
		FinishedAt:       j.FinishedAt,
		UpdatedAt:        j.UpdatedAt,
	}
	if lock != nil && lock.JobID == j.ID {
		state := lock.LockState
		resp.LockState = &state
	}
	return resp
}

// A job on another database answers 404 like a missing one, so job IDs cannot be probed across databases.
func (s *Server) handleAPIJobGet(w http.ResponseWriter, r *http.Request) {
	db := currentDB(r)
	jobID, err := uuid.Parse(chi.URLParam(r, "jobID"))
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Job not found.")
		return
	}
	job, err := s.store.GetJob(r.Context(), jobID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && job.DBID != db.ID) {
		writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Job not found.")
		return
	}
	if err != nil {
		s.logger.Error("load job", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Could not load the job.")
		return
	}

	var lock *domain.DBLock
	l, err := s.store.GetDBLock(r.Context(), db.ID)
	switch {
	case err == nil:
		lock = &l
	case !errors.Is(err, store.ErrNotFound):
		s.logger.Error("load db lock", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Could not load the database lock.")
		return
	}
	writeJSON(w, http.StatusOK, newJobStatusResponse(job, lock))
}
