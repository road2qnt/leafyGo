package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"leafygo/internal/domain"
	"leafygo/internal/store"
)

func apiLogin(t *testing.T, srv *Server, username, password string) (sid, csrf string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("apiLogin(%s) status = %d: %s", username, rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "sid" {
			sid = c.Value
		}
	}
	var resp struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode apiLogin response: %v", err)
	}
	return sid, resp.CSRFToken
}

func apiRequestJSON(t *testing.T, srv *Server, method, path, sid, csrf string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader *bytes.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		bodyReader = bytes.NewReader(raw)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if sid != "" {
		req.AddCookie(&http.Cookie{Name: "sid", Value: sid})
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func assertAPIErrorCode(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d; want %d (body: %s)", rec.Code, wantStatus, rec.Body.String())
	}
	var env apiErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal error envelope: %v (body: %s)", err, rec.Body.String())
	}
	if env.Error.Code != wantCode {
		t.Fatalf("error.code = %q; want %q (body: %s)", env.Error.Code, wantCode, rec.Body.String())
	}
	if env.Error.CorrelationID == "" || env.Error.TraceID == "" {
		t.Fatalf("expected correlation_id and trace_id in error response: %+v", env.Error)
	}
}

func setupTargetDBConn(t *testing.T, srv *Server, st *store.Store, tc *pgx.ConnConfig, name string) domain.DBConnection {
	t.Helper()
	enc, err := srv.box.Encrypt(tc.Password)
	if err != nil {
		t.Fatalf("encrypt target password: %v", err)
	}
	port := int(tc.Port)
	if port == 0 {
		port = 5432
	}
	conn, err := st.CreateDBConnection(context.Background(), domain.DBConnection{
		Name:        name,
		Host:        tc.Host,
		Port:        port,
		DBName:      tc.Database,
		Username:    tc.User,
		PasswordEnc: enc,
		SSLMode:     "disable",
	})
	if err != nil {
		t.Fatalf("CreateDBConnection: %v", err)
	}
	return conn
}

func TestAPIProposalValidationDryRunCatalogAndRBAC(t *testing.T) {
	tc := targetConfig(t)
	srv, st := testServer(t)
	ctx := context.Background()

	seedUser(t, st, "admin", "password123", true)
	editor := seedUser(t, st, "editor", "password123", false)
	viewer := seedUser(t, st, "viewer", "password123", false)

	grpEdit, _ := st.CreateGroup(ctx, "eng-edit", "")
	_ = st.AddGroupMember(ctx, grpEdit.ID, editor.ID)
	_, _ = st.SetGroupLeader(ctx, grpEdit.ID, &editor.ID)

	grpView, _ := st.CreateGroup(ctx, "eng-view", "")
	_ = st.AddGroupMember(ctx, grpView.ID, viewer.ID)
	_, _ = st.SetGroupLeader(ctx, grpView.ID, &viewer.ID)

	dbConn := setupTargetDBConn(t, srv, st, tc, "target-validation-"+ strconv.FormatInt(time.Now().UnixNano(), 10))
	_ = st.SetGroupAccess(ctx, grpEdit.ID, dbConn.ID, domain.LevelEdit)
	_ = st.SetGroupAccess(ctx, grpView.ID, dbConn.ID, domain.LevelView)

	adminSID, adminCSRF := apiLogin(t, srv, "admin", "password123")
	editorSID, editorCSRF := apiLogin(t, srv, "editor", "password123")
	viewerSID, viewerCSRF := apiLogin(t, srv, "viewer", "password123")

	migBase := "/api/v1/databases/" + dbConn.ID.String()

	// 1. Access Denied & CSRF Checks (403 Forbidden)
	defPayload := map[string]any{
		"title":    "create widgets",
		"sql_up":   fmt.Sprintf("CREATE TABLE %s.widgets (id bigint primary key, name text); ALTER TABLE %s.widgets ADD COLUMN qty int; INSERT INTO %s.widgets VALUES (1, 'w1', 10);", testSchema, testSchema, testSchema),
		"sql_down": fmt.Sprintf("DROP TABLE %s.widgets;", testSchema),
	}

	// Missing X-CSRF-Token -> 403
	recNoCSRF := apiRequestJSON(t, srv, http.MethodPost, migBase+"/migrations", editorSID, "", defPayload)
	assertAPIErrorCode(t, recNoCSRF, http.StatusForbidden, ErrCodeInvalidCSRF)

	// LevelView user -> 403
	recViewer := apiRequestJSON(t, srv, http.MethodPost, migBase+"/migrations", viewerSID, viewerCSRF, defPayload)
	assertAPIErrorCode(t, recViewer, http.StatusForbidden, ErrCodeForbidden)

	// PlatformAdmin without group access -> 403
	recAdmin := apiRequestJSON(t, srv, http.MethodPost, migBase+"/migrations", adminSID, adminCSRF, defPayload)
	assertAPIErrorCode(t, recAdmin, http.StatusForbidden, ErrCodeForbidden)

	// 2. Multi-statement DDL + fast DML passes dry-run BEGIN ... ROLLBACK ("passed") and leaves no table in Target DB
	recCreateDef := apiRequestJSON(t, srv, http.MethodPost, migBase+"/migrations", editorSID, editorCSRF, defPayload)
	if recCreateDef.Code != http.StatusCreated {
		t.Fatalf("create definition status = %d: %s", recCreateDef.Code, recCreateDef.Body.String())
	}
	var createdDef domain.MigrationDefinition
	_ = json.Unmarshal(recCreateDef.Body.Bytes(), &createdDef)

	recSubmitPass := apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals", editorSID, editorCSRF, map[string]any{
		"migration_id": createdDef.ID,
		"action_type":  "apply",
	})
	if recSubmitPass.Code != http.StatusCreated {
		t.Fatalf("submit valid DDL proposal status = %d: %s", recSubmitPass.Code, recSubmitPass.Body.String())
	}
	var passResp struct {
		DryRunStatus string                   `json:"dry_run_status"`
		Proposal     domain.MigrationProposal `json:"proposal"`
	}
	_ = json.Unmarshal(recSubmitPass.Body.Bytes(), &passResp)
	if passResp.DryRunStatus != "passed" || passResp.Proposal.Status != domain.ProposalPendingReview {
		t.Fatalf("unexpected dry-run pass response: %+v", passResp)
	}

	// Verify table widgets was rolled back on Target DB
	tConn, err := pgx.ConnectConfig(ctx, tc)
	if err != nil {
		t.Fatalf("connect target: %v", err)
	}
	defer tConn.Close(ctx)
	var widgetsExists bool
	if err := tConn.QueryRow(ctx, `select exists(select 1 from information_schema.tables where table_schema=$1 and table_name='widgets')`, testSchema).Scan(&widgetsExists); err != nil {
		t.Fatalf("check widgets table: %v", err)
	}
	if widgetsExists {
		t.Fatal("widgets table must not exist after dry-run ROLLBACK")
	}

	// Withdraw proposal so we can test more cases cleanly
	recWithdraw := apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals/"+passResp.Proposal.ID.String()+"/withdraw", editorSID, editorCSRF, nil)
	if recWithdraw.Code != http.StatusOK {
		t.Fatalf("withdraw status = %d: %s", recWithdraw.Code, recWithdraw.Body.String())
	}

	// 3. Slow DML (pg_sleep(3.5) / SQLSTATE 57014) AND locked table (SQLSTATE 55P03) pass with dry_run_status = "inconclusive"
	// Hold ACCESS EXCLUSIVE lock on testSchema.items from a separate connection to trigger 55P03 lock_not_available in 1s!
	lockTx, err := tConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin lock tx: %v", err)
	}
	if _, err := lockTx.Exec(ctx, fmt.Sprintf("LOCK TABLE %s.items IN ACCESS EXCLUSIVE MODE;", testSchema)); err != nil {
		_ = lockTx.Rollback(ctx)
		t.Fatalf("lock table items: %v", err)
	}

	recLockDef := apiRequestJSON(t, srv, http.MethodPost, migBase+"/migrations", editorSID, editorCSRF, map[string]any{
		"title":               "update locked items",
		"sql_up":              fmt.Sprintf("UPDATE %s.items SET name = 'locked' WHERE id = 1;", testSchema),
		"is_irreversible":     true,
		"irreversible_reason": "test lock timeout",
	})
	var lockDef domain.MigrationDefinition
	_ = json.Unmarshal(recLockDef.Body.Bytes(), &lockDef)

	recSubmitInconclusive := apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals", editorSID, editorCSRF, map[string]any{
		"migration_id": lockDef.ID,
		"action_type":  "apply",
	})
	_ = lockTx.Rollback(ctx)

	if recSubmitInconclusive.Code != http.StatusCreated {
		t.Fatalf("submit locked table proposal status = %d (want 201 inconclusive): %s", recSubmitInconclusive.Code, recSubmitInconclusive.Body.String())
	}
	var incResp struct {
		DryRunStatus  string                   `json:"dry_run_status"`
		DryRunWarning string                   `json:"dry_run_warning"`
		Proposal      domain.MigrationProposal `json:"proposal"`
	}
	_ = json.Unmarshal(recSubmitInconclusive.Body.Bytes(), &incResp)
	if incResp.DryRunStatus != "inconclusive" || incResp.Proposal.Status != domain.ProposalPendingReview {
		t.Fatalf("expected dry_run_status='inconclusive' and status='pending_review', got %+v", incResp)
	}

	// 4. Two concurrent dry-runs on the same db_id reject the second request with 429 DRY_RUN_BUSY
	recSlowDef1 := apiRequestJSON(t, srv, http.MethodPost, migBase+"/migrations", editorSID, editorCSRF, map[string]any{
		"title":               "slow 1",
		"sql_up":              "SELECT pg_sleep(1.5);",
		"is_irreversible":     true,
		"irreversible_reason": "concurrency test",
	})
	var slowDef1 domain.MigrationDefinition
	_ = json.Unmarshal(recSlowDef1.Body.Bytes(), &slowDef1)

	recSlowDef2 := apiRequestJSON(t, srv, http.MethodPost, migBase+"/migrations", editorSID, editorCSRF, map[string]any{
		"title":               "slow 2",
		"sql_up":              "SELECT pg_sleep(1.5);",
		"is_irreversible":     true,
		"irreversible_reason": "concurrency test",
	})
	var slowDef2 domain.MigrationDefinition
	_ = json.Unmarshal(recSlowDef2.Body.Bytes(), &slowDef2)

	var wg sync.WaitGroup
	recs := make([]*httptest.ResponseRecorder, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		recs[0] = apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals", editorSID, editorCSRF, map[string]any{
			"migration_id": slowDef1.ID,
			"action_type":  "apply",
		})
	}()
	time.Sleep(80 * time.Millisecond)
	go func() {
		defer wg.Done()
		recs[1] = apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals", editorSID, editorCSRF, map[string]any{
			"migration_id": slowDef2.ID,
			"action_type":  "apply",
		})
	}()
	wg.Wait()

	var busyRec *httptest.ResponseRecorder
	if recs[0].Code == http.StatusTooManyRequests {
		busyRec = recs[0]
	} else if recs[1].Code == http.StatusTooManyRequests {
		busyRec = recs[1]
	}
	if busyRec == nil {
		t.Fatalf("expected one concurrent dry-run to fail with 429, got codes (%d, %d)", recs[0].Code, recs[1].Code)
	}
	assertAPIErrorCode(t, busyRec, http.StatusTooManyRequests, ErrCodeDryRunBusy)

	// 5. Syntax / catalog error (42P01) and non-transactional statement (CREATE INDEX CONCURRENTLY)
	recBadSQLDef := apiRequestJSON(t, srv, http.MethodPost, migBase+"/migrations", editorSID, editorCSRF, map[string]any{
		"title":               "bad table ref",
		"sql_up":              fmt.Sprintf("SELECT * FROM %s.missing_table_abc;", testSchema),
		"is_irreversible":     true,
		"irreversible_reason": "test 42P01",
	})
	var badSQLDef domain.MigrationDefinition
	_ = json.Unmarshal(recBadSQLDef.Body.Bytes(), &badSQLDef)

	recBadSQLProp := apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals", editorSID, editorCSRF, map[string]any{
		"migration_id": badSQLDef.ID,
		"action_type":  "apply",
	})
	assertAPIErrorCode(t, recBadSQLProp, http.StatusUnprocessableEntity, ErrCodeInvalidMigrationSQL)

	recNonTxDef := apiRequestJSON(t, srv, http.MethodPost, migBase+"/migrations", editorSID, editorCSRF, map[string]any{
		"title":               "non transactional index",
		"sql_up":              fmt.Sprintf("CREATE INDEX CONCURRENTLY idx_items_name ON %s.items(name);", testSchema),
		"is_irreversible":     true,
		"irreversible_reason": "test non tx",
	})
	var nonTxDef domain.MigrationDefinition
	_ = json.Unmarshal(recNonTxDef.Body.Bytes(), &nonTxDef)

	recNonTxProp := apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals", editorSID, editorCSRF, map[string]any{
		"migration_id": nonTxDef.ID,
		"action_type":  "apply",
	})
	assertAPIErrorCode(t, recNonTxProp, http.StatusUnprocessableEntity, ErrCodeNonTransactionalStmt)

	// 6. Batch mode catalog missing table/column (422 INVALID_BATCH_IDENTIFIER) & non-integer cursor_col (422 INVALID_BATCH_CURSOR_TYPE)
	recBatchMissingDef := apiRequestJSON(t, srv, http.MethodPost, migBase+"/migrations", editorSID, editorCSRF, map[string]any{
		"title":          "batch missing table",
		"sql_up":         fmt.Sprintf("UPDATE %s.no_such_table SET v = 1 WHERE id >= {{cursor_from}} AND id <= {{cursor_to}};", testSchema),
		"sql_down":       fmt.Sprintf("UPDATE %s.no_such_table SET v = 0 WHERE id >= {{cursor_from}} AND id <= {{cursor_to}};", testSchema),
		"execution_mode": "batch",
		"batch_config": map[string]any{
			"table_name": testSchema + ".no_such_table",
			"cursor_col": "id",
			"batch_size": 100,
		},
	})
	var batchMissingDef domain.MigrationDefinition
	_ = json.Unmarshal(recBatchMissingDef.Body.Bytes(), &batchMissingDef)

	recBatchMissingProp := apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals", editorSID, editorCSRF, map[string]any{
		"migration_id": batchMissingDef.ID,
		"action_type":  "apply",
	})
	assertAPIErrorCode(t, recBatchMissingProp, http.StatusUnprocessableEntity, ErrCodeInvalidBatchIdentifier)

	recBatchTextCursorDef := apiRequestJSON(t, srv, http.MethodPost, migBase+"/migrations", editorSID, editorCSRF, map[string]any{
		"title":          "batch text cursor",
		"sql_up":         fmt.Sprintf("UPDATE %s.items SET note = 'x' WHERE id >= {{cursor_from}} AND id <= {{cursor_to}};", testSchema),
		"sql_down":       fmt.Sprintf("UPDATE %s.items SET note = null WHERE id >= {{cursor_from}} AND id <= {{cursor_to}};", testSchema),
		"execution_mode": "batch",
		"batch_config": map[string]any{
			"table_name": testSchema + ".items",
			"cursor_col": "name", // 'name' column is text, not integer!
			"batch_size": 100,
		},
	})
	var batchTextCursorDef domain.MigrationDefinition
	_ = json.Unmarshal(recBatchTextCursorDef.Body.Bytes(), &batchTextCursorDef)

	recBatchTextCursorProp := apiRequestJSON(t, srv, http.MethodPost, migBase+"/proposals", editorSID, editorCSRF, map[string]any{
		"migration_id": batchTextCursorDef.ID,
		"action_type":  "apply",
	})
	assertAPIErrorCode(t, recBatchTextCursorProp, http.StatusUnprocessableEntity, ErrCodeInvalidBatchCursorType)

	// Verify Schema Inspector hides _leafygo_executions and _leafygo_batch_checkpoints via GET /api/v1/databases/{id}/schema
	if _, err := tConn.Exec(ctx, fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s._leafygo_executions (execution_id uuid primary key);", testSchema)); err != nil {
		t.Fatalf("create _leafygo_executions on target: %v", err)
	}
	recSchema := apiRequestJSON(t, srv, http.MethodGet, migBase+"/schema", viewerSID, "", nil)
	if recSchema.Code != http.StatusOK {
		t.Fatalf("GET /schema status = %d: %s", recSchema.Code, recSchema.Body.String())
	}
	if strings.Contains(recSchema.Body.String(), "_leafygo_") {
		t.Fatalf("GET /schema leaked internal _leafygo_ table: %s", recSchema.Body.String())
	}
}
