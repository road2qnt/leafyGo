package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"leafygo/internal/domain"
)

func TestAPIApprovalWorkflowQuorumIntersectionPredecessorGateAndHoldProposals(t *testing.T) {
	tc := targetConfig(t)
	srv, st := testServer(t)
	ctx := context.Background()

	seedUser(t, st, "admin", "password123", true)
	leaderEdit := seedUser(t, st, "leader_edit", "password123", false)
	leaderView := seedUser(t, st, "leader_view", "password123", false)
	leaderExtra := seedUser(t, st, "leader_extra", "password123", false)
	member := seedUser(t, st, "member", "password123", false)

	grpEdit, _ := st.CreateGroup(ctx, "grp-edit", "")
	_ = st.AddGroupMember(ctx, grpEdit.ID, leaderEdit.ID)
	_ = st.AddGroupMember(ctx, grpEdit.ID, member.ID)
	_, _ = st.SetGroupLeader(ctx, grpEdit.ID, &leaderEdit.ID)

	grpView, _ := st.CreateGroup(ctx, "grp-view", "")
	_ = st.AddGroupMember(ctx, grpView.ID, leaderView.ID)
	_, _ = st.SetGroupLeader(ctx, grpView.ID, &leaderView.ID)

	grpExtra, _ := st.CreateGroup(ctx, "grp-extra", "")
	_ = st.AddGroupMember(ctx, grpExtra.ID, leaderExtra.ID)
	_, _ = st.SetGroupLeader(ctx, grpExtra.ID, &leaderExtra.ID)

	dbConn := setupTargetDBConn(t, srv, st, tc, "target-approval-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	_ = st.SetGroupAccess(ctx, grpEdit.ID, dbConn.ID, domain.LevelEdit)
	_ = st.SetGroupAccess(ctx, grpView.ID, dbConn.ID, domain.LevelView)
	_ = st.SetGroupAccess(ctx, grpExtra.ID, dbConn.ID, domain.LevelView)

	leSID, leCSRF := apiLogin(t, srv, "leader_edit", "password123")
	lvSID, lvCSRF := apiLogin(t, srv, "leader_view", "password123")
	memSID, memCSRF := apiLogin(t, srv, "member", "password123")

	migBase := "/api/v1/databases/" + dbConn.ID.String()

	// Create 2 definitions: slot 1 and slot 2
	recDef1 := apiRequestJSON(t, srv, http.MethodPost, migBase+"/migrations", leSID, leCSRF, map[string]any{
		"title":          "mig slot 1",
		"sql_up":         fmt.Sprintf("UPDATE %s.items SET note = 'b' WHERE id >= {{cursor_from}} AND id <= {{cursor_to}};", testSchema),
		"sql_down":       fmt.Sprintf("UPDATE %s.items SET note = null WHERE id >= {{cursor_from}} AND id <= {{cursor_to}};", testSchema),
		"execution_mode": "batch",
		"batch_config": map[string]any{
			"table_name": testSchema + ".items",
			"cursor_col": "id",
			"batch_size": 10,
		},
	})
	if recDef1.Code != http.StatusCreated {
		t.Fatalf("create def1 = %d: %s", recDef1.Code, recDef1.Body.String())
	}
	var def1 domain.MigrationDefinition
	_ = json.Unmarshal(recDef1.Body.Bytes(), &def1)

	recDef2 := apiRequestJSON(t, srv, http.MethodPost, migBase+"/migrations", leSID, leCSRF, map[string]any{
		"title":    "mig slot 2",
		"sql_up":   fmt.Sprintf("CREATE TABLE %s.slot2_tbl (id int primary key);", testSchema),
		"sql_down": fmt.Sprintf("DROP TABLE %s.slot2_tbl;", testSchema),
	})
	var def2 domain.MigrationDefinition
	_ = json.Unmarshal(recDef2.Body.Bytes(), &def2)

	// Submit proposals for slot 1 and slot 2
	recProp1 := apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals", leSID, leCSRF, map[string]any{
		"migration_id": def1.ID,
		"action_type":  "apply",
	})
	if recProp1.Code != http.StatusCreated {
		t.Fatalf("submit prop1 = %d: %s", recProp1.Code, recProp1.Body.String())
	}
	var prop1Wrap struct {
		Proposal domain.MigrationProposal `json:"proposal"`
	}
	_ = json.Unmarshal(recProp1.Body.Bytes(), &prop1Wrap)
	prop1 := prop1Wrap.Proposal

	// Duplicate active proposal while pending_review -> 409 DUPLICATE_ACTIVE_PROPOSAL
	recDupProp := apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals", leSID, leCSRF, map[string]any{
		"migration_id": def1.ID,
		"action_type":  "apply",
	})
	assertAPIErrorCode(t, recDupProp, http.StatusConflict, ErrCodeDuplicateActiveProposal)

	// Editing definition while active proposal exists -> 409 DUPLICATE_ACTIVE_PROPOSAL (blocked by handler & trigger)
	recEditLocked := apiRequestJSON(t, srv, http.MethodPut, migBase+"/migrations/"+def1.ID.String(), leSID, leCSRF, map[string]any{
		"title":          "mig slot 1 edited",
		"sql_up":         def1.SQLUp,
		"sql_down":       def1.SQLDown,
		"execution_mode": "batch",
		"batch_config":   def1.BatchConfig,
	})
	assertAPIErrorCode(t, recEditLocked, http.StatusConflict, ErrCodeDuplicateActiveProposal)

	// Direct DB UPDATE to sql_up while active proposal exists is rejected by trg_migration_definitions_immutable
	if _, err := st.Pool().Exec(ctx, `update migration_definitions set sql_up = 'SELECT 99;' where id = $1`, def1.ID); err == nil {
		t.Fatal("expected trg_migration_definitions_immutable to block direct SQL update while active proposal exists")
	}

	// Leader approves, then author withdraws -> invalidates approval, increments revision, allows editing again
	recFirstApp := apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals/"+prop1.ID.String()+"/approve", leSID, leCSRF, map[string]any{"comment": "ok"})
	if recFirstApp.Code != http.StatusOK {
		t.Fatalf("first approve before withdraw = %d: %s", recFirstApp.Code, recFirstApp.Body.String())
	}
	recWithdraw := apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals/"+prop1.ID.String()+"/withdraw", leSID, leCSRF, nil)
	if recWithdraw.Code != http.StatusOK {
		t.Fatalf("withdraw = %d: %s", recWithdraw.Code, recWithdraw.Body.String())
	}

	recEditAfterWithdraw := apiRequestJSON(t, srv, http.MethodPut, migBase+"/migrations/"+def1.ID.String(), leSID, leCSRF, map[string]any{
		"title":          "mig slot 1 v2",
		"sql_up":         def1.SQLUp,
		"sql_down":       def1.SQLDown,
		"execution_mode": "batch",
		"batch_config":   def1.BatchConfig,
	})
	if recEditAfterWithdraw.Code != http.StatusOK {
		t.Fatalf("edit after withdraw = %d: %s", recEditAfterWithdraw.Code, recEditAfterWithdraw.Body.String())
	}

	// Re-submit prop1 and submit prop2
	recProp1 = apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals", leSID, leCSRF, map[string]any{
		"migration_id": def1.ID,
		"action_type":  "apply",
	})
	_ = json.Unmarshal(recProp1.Body.Bytes(), &prop1Wrap)
	prop1 = prop1Wrap.Proposal

	recProp2 := apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals", leSID, leCSRF, map[string]any{
		"migration_id": def2.ID,
		"action_type":  "apply",
	})
	var prop2Wrap struct {
		Proposal domain.MigrationProposal `json:"proposal"`
	}
	_ = json.Unmarshal(recProp2.Body.Bytes(), &prop2Wrap)
	prop2 := prop2Wrap.Proposal

	// Revoke grpExtra's access after submission -> quorum intersection becomes {grpEdit, grpView}
	if err := st.RemoveGroupAccess(ctx, grpExtra.ID, dbConn.ID); err != nil {
		t.Fatalf("RemoveGroupAccess grpExtra: %v", err)
	}

	// Non-leader member trying to approve -> 403 FORBIDDEN_NOT_GROUP_LEADER
	recNonLeader := apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals/"+prop1.ID.String()+"/approve", memSID, memCSRF, nil)
	assertAPIErrorCode(t, recNonLeader, http.StatusForbidden, ErrCodeForbiddenNotGroupLeader)

	// Out-of-order final approval on prop2 (slot 2) while slot 1 is still pending_review -> 409 PREDECESSOR_NOT_READY
	apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals/"+prop2.ID.String()+"/approve", leSID, leCSRF, nil)
	recOutOfOrder := apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals/"+prop2.ID.String()+"/approve", lvSID, lvCSRF, nil)
	assertAPIErrorCode(t, recOutOfOrder, http.StatusConflict, ErrCodePredecessorNotReady)

	// Verify prop2 stays in pending_review
	recGetProp2 := apiRequestJSON(t, srv, http.MethodGet, migBase+"/proposals/"+prop2.ID.String(), leSID, "", nil)
	var prop2AfterGate domain.MigrationProposal
	_ = json.Unmarshal(recGetProp2.Body.Bytes(), &prop2AfterGate)
	if prop2AfterGate.Status != domain.ProposalPendingReview {
		t.Fatalf("prop2 status after predecessor gate rejection = %q; want pending_review", prop2AfterGate.Status)
	}

	// Approve prop1 (slot 1): leaderEdit approves, then 2 concurrent requests from leaderView -> exactly 1 job created
	apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals/"+prop1.ID.String()+"/approve", leSID, leCSRF, nil)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var okResponses []map[string]any
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals/"+prop1.ID.String()+"/approve", lvSID, lvCSRF, nil)
			if rec.Code == http.StatusOK {
				var m map[string]any
				_ = json.Unmarshal(rec.Body.Bytes(), &m)
				mu.Lock()
				okResponses = append(okResponses, m)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(okResponses) != 1 {
		t.Fatalf("concurrent final approval 200 OK count = %d; want 1", len(okResponses))
	}
	if okResponses[0]["status"] != "queued" || okResponses[0]["job_id"] == nil || okResponses[0]["execution_id"] == nil {
		t.Fatalf("unexpected enqueue response: %+v", okResponses[0])
	}
	exec1ID, _ := uuid.Parse(okResponses[0]["execution_id"].(string))

	// Hold-State Recovery Proposals: transition prop1/exec1/job1 to 'partial' (and mark def1 immutable),
	// then verify submitting batch_retry / batch_revert on the same migration_id SUCCEEDS (no 409 DUPLICATE_ACTIVE_PROPOSAL)!
	if _, err := st.Pool().Exec(ctx, `
		update migration_definitions set is_immutable = true where id = $1;
		update migration_proposals set status = 'partial' where id = $2;
		update migration_executions set status = 'partial' where execution_id = $3;
		update migration_jobs set status = 'partial' where execution_id = $3;
	`, def1.ID, prop1.ID, exec1ID); err != nil {
		t.Fatalf("set partial hold state: %v", err)
	}

	recRetryProp := apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals", leSID, leCSRF, map[string]any{
		"migration_id":        def1.ID,
		"action_type":         "batch_retry",
		"target_execution_id": exec1ID,
	})
	if recRetryProp.Code != http.StatusCreated {
		t.Fatalf("submit batch_retry on partial migration = %d (want 201): %s", recRetryProp.Code, recRetryProp.Body.String())
	}
}

