package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"leafygo/internal/domain"
)

func TestAPIRollbackForwardEffectiveSeqRelaxedSupersedeAndDeletionPolicies(t *testing.T) {
	tc := targetConfig(t)
	srv, st := testServer(t)
	ctx := context.Background()

	seedUser(t, st, "admin", "password123", true)
	leader := seedUser(t, st, "leader", "password123", false)
	executorOnly := seedUser(t, st, "exec_only", "password123", false)

	grp, _ := st.CreateGroup(ctx, "core-dba", "")
	_ = st.AddGroupMember(ctx, grp.ID, leader.ID)
	_, _ = st.SetGroupLeader(ctx, grp.ID, &leader.ID)

	dbName := "target-rollback-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	dbConn := setupTargetDBConn(t, srv, st, tc, dbName)
	_ = st.SetGroupAccess(ctx, grp.ID, dbConn.ID, domain.LevelEdit)

	adminSID, adminCSRF := apiLogin(t, srv, "admin", "password123")
	leaderSID, leaderCSRF := apiLogin(t, srv, "leader", "password123")
	migBase := "/api/v1/databases/" + dbConn.ID.String()

	createDefAPI := func(title, sqlUp, sqlDown string, replaces *uuid.UUID) domain.MigrationDefinition {
		t.Helper()
		payload := map[string]any{
			"title":    title,
			"sql_up":   sqlUp,
			"sql_down": sqlDown,
		}
		if replaces != nil {
			payload["replaces_migration_id"] = *replaces
		}
		rec := apiRequestJSON(t, srv, http.MethodPost, migBase+"/migrations", leaderSID, leaderCSRF, payload)
		if rec.Code != http.StatusCreated {
			t.Fatalf("createDefAPI(%s) = %d: %s", title, rec.Code, rec.Body.String())
		}
		var d domain.MigrationDefinition
		_ = json.Unmarshal(rec.Body.Bytes(), &d)
		return d
	}

	// 1. Define A(1), B(2), C(3)
	migA := createDefAPI("A", fmt.Sprintf("SELECT 1 FROM %s.items;", testSchema), fmt.Sprintf("SELECT 1 FROM %s.items;", testSchema), nil)
	migB := createDefAPI("B", fmt.Sprintf("SELECT 2 FROM %s.items;", testSchema), fmt.Sprintf("SELECT 2 FROM %s.items;", testSchema), nil)
	migC := createDefAPI("C", fmt.Sprintf("SELECT 3 FROM %s.items;", testSchema), fmt.Sprintf("SELECT 3 FROM %s.items;", testSchema), nil)

	// Mark A as applied (success), B as failed (immutable)
	execA, err := st.InsertMigrationExecution(ctx, domain.MigrationExecution{
		DBID: dbConn.ID, MigrationID: migA.ID, MigrationRevision: 1,
		ActionType: domain.ActionApply, ExecutedSQL: migA.SQLUp,
		ExecutedBy: &executorOnly.ID, ExecutedByUsername: executorOnly.Username,
		DBNameSnapshot: dbConn.Name, MigrationTitleSnapshot: migA.Title,
		Status: domain.StatusSuccess,
	})
	if err != nil {
		t.Fatalf("insert execA: %v", err)
	}
	_, err = st.InsertMigrationExecution(ctx, domain.MigrationExecution{
		DBID: dbConn.ID, MigrationID: migB.ID, MigrationRevision: 1,
		ActionType: domain.ActionApply, ExecutedSQL: migB.SQLUp,
		ExecutedBy: &leader.ID, ExecutedByUsername: leader.Username,
		DBNameSnapshot: dbConn.Name, MigrationTitleSnapshot: migB.Title,
		Status: domain.StatusFailed, ErrorMessage: "failed B",
	})
	if err != nil {
		t.Fatalf("insert execB: %v", err)
	}
	_, _ = st.Pool().Exec(ctx, `update migration_definitions set is_immutable = true where id in ($1, $2)`, migA.ID, migB.ID)

	// Attempting to supersede Active migration A -> 409 MIGRATION_NOT_SUPERSEDABLE
	recSupActive := apiRequestJSON(t, srv, http.MethodPost, migBase+"/migrations", leaderSID, leaderCSRF, map[string]any{
		"title":                 "A-prime",
		"sql_up":                "SELECT 1;",
		"sql_down":              "SELECT 1;",
		"replaces_migration_id": migA.ID,
	})
	assertAPIErrorCode(t, recSupActive, http.StatusConflict, ErrCodeMigrationNotSupersedable)

	// Create B'(raw seq=4, effective_seq=2) replacing failed B(2)
	migBPrime := createDefAPI("B-prime", fmt.Sprintf("SELECT 22 FROM %s.items;", testSchema), fmt.Sprintf("SELECT 22 FROM %s.items;", testSchema), &migB.ID)
	if migBPrime.SeqVersion != 4 || migBPrime.EffectiveSeq != 2 {
		t.Fatalf("B' seq=%d eff=%d; want 4, 2", migBPrime.SeqVersion, migBPrime.EffectiveSeq)
	}

	// Activate B'(eff=2) then C(eff=3)
	execBPrime, _ := st.InsertMigrationExecution(ctx, domain.MigrationExecution{
		DBID: dbConn.ID, MigrationID: migBPrime.ID, MigrationRevision: 1,
		ActionType: domain.ActionApply, ExecutedSQL: migBPrime.SQLUp,
		ExecutedBy: &leader.ID, ExecutedByUsername: leader.Username,
		DBNameSnapshot: dbConn.Name, MigrationTitleSnapshot: migBPrime.Title,
		Status: domain.StatusSuccess,
	})
	_ = st.MarkSupersededChain(ctx, dbConn.ID, migBPrime.EffectiveSeq, migBPrime.ID)
	execC, _ := st.InsertMigrationExecution(ctx, domain.MigrationExecution{
		DBID: dbConn.ID, MigrationID: migC.ID, MigrationRevision: 1,
		ActionType: domain.ActionApply, ExecutedSQL: migC.SQLUp,
		ExecutedBy: &leader.ID, ExecutedByUsername: leader.Username,
		DBNameSnapshot: dbConn.Name, MigrationTitleSnapshot: migC.Title,
		Status: domain.StatusSuccess,
	})
	_, _ = st.Pool().Exec(ctx, `update migration_definitions set is_immutable = true where id in ($1, $2)`, migBPrime.ID, migC.ID)

	// 2. LIFO Rollback must select C(eff=3) first, NOT B'(raw seq=4, eff=2)!
	// Out-of-order rollback approval on B'(eff=2) while C(eff=3) is still Active -> 409 PREDECESSOR_NOT_READY
	recPropRbBPrime := apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals", leaderSID, leaderCSRF, map[string]any{
		"migration_id":        migBPrime.ID,
		"action_type":         "rollback",
		"target_execution_id": execBPrime.ExecutionID,
	})
	if recPropRbBPrime.Code != http.StatusCreated {
		t.Fatalf("submit rollback B' = %d: %s", recPropRbBPrime.Code, recPropRbBPrime.Body.String())
	}
	var propRbBPrimeWrap struct {
		Proposal domain.MigrationProposal `json:"proposal"`
	}
	_ = json.Unmarshal(recPropRbBPrime.Body.Bytes(), &propRbBPrimeWrap)

	recAppRbBPrimeEarly := apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals/"+propRbBPrimeWrap.Proposal.ID.String()+"/approve", leaderSID, leaderCSRF, nil)
	assertAPIErrorCode(t, recAppRbBPrimeEarly, http.StatusConflict, ErrCodePredecessorNotReady)
	apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals/"+propRbBPrimeWrap.Proposal.ID.String()+"/withdraw", leaderSID, leaderCSRF, nil)

	// Record successful rollback of C(eff=3) and B'(eff=2)
	_, _ = st.InsertMigrationExecution(ctx, domain.MigrationExecution{
		DBID: dbConn.ID, MigrationID: migC.ID, MigrationRevision: 1,
		ActionType: domain.ActionRollback, ExecutedSQL: migC.SQLDown,
		ExecutedBy: &leader.ID, ExecutedByUsername: leader.Username,
		DBNameSnapshot: dbConn.Name, MigrationTitleSnapshot: migC.Title,
		Status: domain.StatusSuccess, RollbackOfExecutionID: &execC.ExecutionID,
	})
	_, _ = st.InsertMigrationExecution(ctx, domain.MigrationExecution{
		DBID: dbConn.ID, MigrationID: migBPrime.ID, MigrationRevision: 1,
		ActionType: domain.ActionRollback, ExecutedSQL: migBPrime.SQLDown,
		ExecutedBy: &leader.ID, ExecutedByUsername: leader.Username,
		DBNameSnapshot: dbConn.Name, MigrationTitleSnapshot: migBPrime.Title,
		Status: domain.StatusSuccess, RollbackOfExecutionID: &execBPrime.ExecutionID,
	})

	// Verify GET /api/v1/databases/{id}/migrations reports next_forward_target = B'(eff=2) before C(eff=3)
	recListMigs := apiRequestJSON(t, srv, http.MethodGet, migBase+"/migrations", leaderSID, "", nil)
	if recListMigs.Code != http.StatusOK {
		t.Fatalf("GET /migrations = %d: %s", recListMigs.Code, recListMigs.Body.String())
	}
	var migListResp struct {
		NextForwardTarget *domain.MigrationDefinition `json:"next_forward_target"`
	}
	_ = json.Unmarshal(recListMigs.Body.Bytes(), &migListResp)
	if migListResp.NextForwardTarget == nil || migListResp.NextForwardTarget.ID != migBPrime.ID {
		t.Fatalf("next_forward_target = %+v; want B'(eff=2)", migListResp.NextForwardTarget)
	}

	// 3. Relaxed Supersede: B'(eff=2) had apply->success, then rollback->success, then forward->failed.
	// Creating B'' replacing B' MUST succeed!
	_, _ = st.InsertMigrationExecution(ctx, domain.MigrationExecution{
		DBID: dbConn.ID, MigrationID: migBPrime.ID, MigrationRevision: 1,
		ActionType: domain.ActionForward, ExecutedSQL: migBPrime.SQLUp,
		ExecutedBy: &leader.ID, ExecutedByUsername: leader.Username,
		DBNameSnapshot: dbConn.Name, MigrationTitleSnapshot: migBPrime.Title,
		Status: domain.StatusFailed, ErrorMessage: "forward failed",
	})
	migB2 := createDefAPI("B-double-prime", fmt.Sprintf("SELECT 222 FROM %s.items;", testSchema), fmt.Sprintf("SELECT 222 FROM %s.items;", testSchema), &migBPrime.ID)
	if migB2.SeqVersion != 5 || migB2.EffectiveSeq != 2 {
		t.Fatalf("B'' seq=%d eff=%d; want 5, 2", migB2.SeqVersion, migB2.EffectiveSeq)
	}

	// 4. Verify GET /api/v1/databases/{id}/history?hide_rollback=false vs true
	recHistAll := apiRequestJSON(t, srv, http.MethodGet, migBase+"/history?hide_rollback=false", leaderSID, "", nil)
	var histAll struct {
		Executions []domain.MigrationExecution `json:"executions"`
	}
	_ = json.Unmarshal(recHistAll.Body.Bytes(), &histAll)
	if len(histAll.Executions) != 7 {
		t.Fatalf("history?hide_rollback=false len = %d; want 7", len(histAll.Executions))
	}

	recHistHidden := apiRequestJSON(t, srv, http.MethodGet, migBase+"/history?hide_rollback=true", leaderSID, "", nil)
	var histHidden struct {
		Executions []domain.MigrationExecution `json:"executions"`
	}
	_ = json.Unmarshal(recHistHidden.Body.Bytes(), &histHidden)
	// Hidden pairs: (execBPrime apply, rollback B') and (execC apply, rollback C) -> 4 hidden, 3 remaining (execA success, B apply failed, B' forward failed)
	if len(histHidden.Executions) != 3 {
		t.Fatalf("history?hide_rollback=true len = %d; want 3", len(histHidden.Executions))
	}

	// 5. Deletion Policies via /api/v1/admin/* & Trigger trg_migration_executions_immutable
	// Deleting user with proposal/definition history -> 409 USER_HAS_HISTORY
	recDelLeader := apiRequestJSON(t, srv, http.MethodDelete, "/api/v1/admin/users/"+leader.ID.String(), adminSID, adminCSRF, nil)
	assertAPIErrorCode(t, recDelLeader, http.StatusConflict, ErrCodeUserHasHistory)

	// Deleting user who only appears in migration_executions.executed_by succeeds and sets executed_by = NULL while keeping executed_by_username
	recDelExecOnly := apiRequestJSON(t, srv, http.MethodDelete, "/api/v1/admin/users/"+executorOnly.ID.String(), adminSID, adminCSRF, nil)
	if recDelExecOnly.Code != http.StatusOK {
		t.Fatalf("delete executor_only user = %d: %s", recDelExecOnly.Code, recDelExecOnly.Body.String())
	}
	execAAfterDel, err := st.GetMigrationExecution(ctx, dbConn.ID, execA.ExecutionID)
	if err != nil || execAAfterDel.ExecutedBy != nil || execAAfterDel.ExecutedByUsername != "exec_only" {
		t.Fatalf("execA after user delete = %+v, %v", execAAfterDel, err)
	}

	// Deleting group with approver history -> 409 GROUP_HAS_HISTORY
	recDelGroup := apiRequestJSON(t, srv, http.MethodDelete, "/api/v1/admin/groups/"+grp.ID.String(), adminSID, adminCSRF, nil)
	assertAPIErrorCode(t, recDelGroup, http.StatusConflict, ErrCodeGroupHasHistory)

	// Deleting DB while active lock/job exists -> 409 DB_HAS_ACTIVE_WORK
	propActiveID := uuid.New()
	jobActiveID := uuid.New()
	_, _ = st.Pool().Exec(ctx, `
		insert into migration_proposals (id, db_id, migration_id, migration_revision, action_type, status)
		values ($1, $2, $3, 1, 'apply', 'running');
		insert into migration_jobs (job_id, db_id, proposal_id, migration_id, execution_id, action_type, execution_mode, status)
		values ($4, $2, $1, $3, $5, 'apply', 'normal', 'running');
	`, propActiveID, dbConn.ID, migB2.ID, jobActiveID, execA.ExecutionID)

	recDelDBBusy := apiRequestJSON(t, srv, http.MethodDelete, "/api/v1/admin/databases/"+dbConn.ID.String(), adminSID, adminCSRF, nil)
	assertAPIErrorCode(t, recDelDBBusy, http.StatusConflict, ErrCodeDBHasActiveWork)

	// Complete job -> Delete DB performs soft-delete and allows reusing the same DB name!
	_, _ = st.Pool().Exec(ctx, `update migration_jobs set status = 'success' where job_id = $1`, jobActiveID)
	recDelDBOk := apiRequestJSON(t, srv, http.MethodDelete, "/api/v1/admin/databases/"+dbConn.ID.String(), adminSID, adminCSRF, nil)
	if recDelDBOk.Code != http.StatusOK {
		t.Fatalf("soft-delete DB = %d: %s", recDelDBOk.Code, recDelDBOk.Body.String())
	}

	recReuseDB := apiRequestJSON(t, srv, http.MethodPost, "/api/v1/admin/databases", adminSID, adminCSRF, map[string]any{
		"name":     dbName,
		"host":     tc.Host,
		"port":     int(tc.Port),
		"dbname":   tc.Database,
		"username": tc.User,
		"password": tc.Password,
		"sslmode":  "disable",
	})
	if recReuseDB.Code != http.StatusCreated {
		t.Fatalf("reuse soft-deleted DB name = %d: %s", recReuseDB.Code, recReuseDB.Body.String())
	}
}

