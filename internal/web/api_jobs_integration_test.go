package web

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"leafygo/internal/domain"
	"leafygo/internal/store"
)

func seedQueuedJob(t *testing.T, st *store.Store, dbID uuid.UUID) domain.MigrationJob {
	t.Helper()
	ctx := context.Background()
	migrationID, proposalID, executionID := uuid.New(), uuid.New(), uuid.New()
	pool := st.Pool()
	if _, err := pool.Exec(ctx, `
		insert into migration_definitions (id, db_id, seq_version, effective_seq, title, sql_up, sql_down)
		values ($1, $2, 1, 1, 'm', 'select 1', 'select 1')`, migrationID, dbID); err != nil {
		t.Fatalf("insert definition: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into migration_proposals (id, db_id, migration_id, migration_revision, action_type, status)
		values ($1, $2, $3, 1, 'apply', 'queued')`, proposalID, dbID, migrationID); err != nil {
		t.Fatalf("insert proposal: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into migration_executions (
			execution_id, root_execution_id, db_id, migration_id, proposal_id, migration_revision, action_type,
			executed_sql, executed_by_username, db_name_snapshot, migration_title_snapshot, status)
		values ($1, $1, $2, $3, $4, 1, 'apply', 'select 1', 'tester', 'db', 'm', 'queued')`,
		executionID, dbID, migrationID, proposalID); err != nil {
		t.Fatalf("insert execution: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	job, err := st.EnqueueMigrationJobTx(ctx, tx, store.EnqueueInput{
		DBID: dbID, ProposalID: proposalID, MigrationID: migrationID, ExecutionID: executionID,
		RootExecutionID: executionID, ActionType: domain.ActionApply, ExecutionMode: domain.ModeNormal,
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return job
}

func grantAccess(t *testing.T, st *store.Store, groupName string, user domain.User, dbID uuid.UUID, level domain.AccessLevel) {
	t.Helper()
	ctx := context.Background()
	g, err := st.CreateGroup(ctx, groupName, "")
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := st.AddGroupMember(ctx, g.ID, user.ID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if err := st.SetGroupAccess(ctx, g.ID, dbID, level); err != nil {
		t.Fatalf("set access: %v", err)
	}
}

func decodeAPIErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var payload struct {
		Error apiError `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}
	return payload.Error.Code
}

func TestAPIJobStatus(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := seedUser(t, st, "budi", "password123", false)
	outsider := seedUser(t, st, "citra", "password123", false)
	seedUser(t, st, "admin", "password123", true)
	orders := seedDBConnection(t, st, "orders")
	billing := seedDBConnection(t, st, "billing")
	grantAccess(t, st, "analis", viewer, orders.ID, domain.LevelView)
	grantAccess(t, st, "keuangan", viewer, billing.ID, domain.LevelView)
	grantAccess(t, st, "lain", outsider, billing.ID, domain.LevelEdit)

	job := seedQueuedJob(t, st, orders.ID)
	claimed, err := st.ClaimNextJob(ctx, "w1", 15*time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}

	viewerSID, _ := login(t, srv, "budi", "password123")
	outsiderSID, _ := login(t, srv, "citra", "password123")
	adminSID, _ := login(t, srv, "admin", "password123")
	path := "/api/v1/databases/" + orders.ID.String() + "/jobs/" + job.ID.String()

	t.Run("viewer sees job", func(t *testing.T) {
		rec := getWithSession(t, srv, path, viewerSID)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
		}
		var got jobStatusResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.JobID != job.ID || got.Status != domain.JobRunning || got.RootExecutionID != job.ExecutionID {
			t.Fatalf("job = %+v", got)
		}
		if got.LockState == nil || *got.LockState != domain.LockRunning {
			t.Fatalf("lock_state = %v; want running", got.LockState)
		}
	})

	cases := []struct {
		name, path, sid string
		status          int
		code            string
	}{
		{"job under another database", "/api/v1/databases/" + billing.ID.String() + "/jobs/" + job.ID.String(), viewerSID, http.StatusNotFound, "JOB_NOT_FOUND"},
		{"unknown job", "/api/v1/databases/" + orders.ID.String() + "/jobs/" + uuid.NewString(), viewerSID, http.StatusNotFound, "JOB_NOT_FOUND"},
		{"malformed job id", "/api/v1/databases/" + orders.ID.String() + "/jobs/nope", viewerSID, http.StatusNotFound, "JOB_NOT_FOUND"},
		{"unknown database", "/api/v1/databases/" + uuid.NewString() + "/jobs/" + job.ID.String(), viewerSID, http.StatusNotFound, "DB_NOT_FOUND"},
		{"user without access", path, outsiderSID, http.StatusForbidden, "FORBIDDEN"},
		{"platform admin", path, adminSID, http.StatusForbidden, "FORBIDDEN"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := getWithSession(t, srv, c.path, c.sid)
			if rec.Code != c.status {
				t.Fatalf("status = %d; want %d (body %s)", rec.Code, c.status, rec.Body)
			}
			if code := decodeAPIErrorCode(t, rec.Body.Bytes()); code != c.code {
				t.Fatalf("code = %q; want %q", code, c.code)
			}
		})
	}
}
