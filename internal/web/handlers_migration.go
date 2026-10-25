package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"leafygo/internal/domain"
	"leafygo/internal/migration"
	"leafygo/internal/store"
	"leafygo/internal/target"
)

const (
	historyLimit  = 200
	maxTitleLen   = 200
	explainPrefix = "EXPLAIN (FORMAT TEXT) "

	msgDraftConflict = "Someone else changed this draft since you opened it. Reload the page and apply your changes again."
	msgTxControl     = "Transactions are managed for you. Remove BEGIN, COMMIT, ROLLBACK, and similar statements."
)

func toDraftRow(d domain.MigrationDraft) draftRow {
	return draftRow{
		ID: d.ID, Title: d.Title, Revision: d.Revision, CreatedByName: d.CreatedByName,
		UpdatedByName: d.UpdatedByName, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	db := currentDB(r)
	items, err := s.store.ListMigrationHistory(ctx, db.ID, historyLimit)
	if err != nil {
		s.serverError(w, r, "list history", err)
		return
	}
	var v migrationsView
	for _, h := range items {
		v.Items = append(v.Items, toHistoryRow(h))
	}
	if raw := r.URL.Query().Get("migration"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			s.renderError(w, r, http.StatusNotFound, "Migration not found.")
			return
		}
		h, err := s.store.GetMigrationHistory(ctx, db.ID, id)
		if errors.Is(err, store.ErrNotFound) {
			s.renderError(w, r, http.StatusNotFound, "Migration not found.")
			return
		}
		if err != nil {
			s.serverError(w, r, "get history", err)
			return
		}
		v.Selected = &historyDetail{historyRow: toHistoryRow(h), SQL: h.SQLText}
	}
	if currentLevel(r).CanEdit() {
		drafts, err := s.store.ListMigrationDrafts(ctx, db.ID)
		if err != nil {
			s.serverError(w, r, "list drafts", err)
			return
		}
		v.DraftCount = len(drafts)
	}
	s.render(w, r, http.StatusOK, "migration/history.html", s.dbPageData(r, "Migration", navDBMigrations, v))
}

func (s *Server) handleDrafts(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.store.ListMigrationDrafts(r.Context(), currentDB(r).ID)
	if err != nil {
		s.serverError(w, r, "list drafts", err)
		return
	}
	var v draftsView
	for _, d := range drafts {
		v.Drafts = append(v.Drafts, toDraftRow(d))
	}
	s.render(w, r, http.StatusOK, "migration/drafts.html", s.dbPageData(r, "Drafts", navDBDrafts, v))
}

func (s *Server) handleDraftNew(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "migration/draft_new.html", s.dbPageData(r, "New draft", navDBDrafts, draftNewView{}))
}

type draftInput struct {
	Title string
	SQL   string
	Token int
}

func readDraftInput(r *http.Request) draftInput {
	token, _ := strconv.Atoi(r.FormValue("token"))
	return draftInput{
		Title: strings.TrimSpace(r.FormValue("title")),
		SQL:   strings.ReplaceAll(r.FormValue("sql"), "\r\n", "\n"),
		Token: token,
	}
}

func (s *Server) validateDraft(in draftInput) map[string]string {
	errs := map[string]string{}
	if utf8.RuneCountInString(in.Title) > maxTitleLen {
		errs["title"] = "Title must be at most 200 characters."
	}
	switch {
	case strings.TrimSpace(in.SQL) == "":
		errs["sql"] = "SQL must not be empty."
	case int64(len(in.SQL)) > s.cfg.MaxMigrationBytes:
		errs["sql"] = fmt.Sprintf("SQL must be at most %s bytes.", formatInt(s.cfg.MaxMigrationBytes))
	case strings.ContainsRune(in.SQL, 0):
		errs["sql"] = "SQL must not contain NUL characters."
	}
	return errs
}

// Baris statement pertama yang berisi kontrol transaksi, 0 bila tidak ada.
func txControlLine(sql string) int {
	stmts, err := migration.Statements(sql)
	if err != nil {
		return 0
	}
	offset := 0
	for _, stmt := range stmts {
		idx := strings.Index(sql[offset:], stmt)
		if idx < 0 {
			return 0
		}
		start := offset + idx
		offset = start + len(stmt)
		if migration.RejectTxControl([]string{stmt}) != nil {
			lead := len(stmt) - len(migration.StripLeadingCommentsAndWhitespace(stmt))
			return sqlLineAt(sql, utf8.RuneCountInString(sql[:start+lead])+1)
		}
	}
	return 0
}

func validationMessage(err error) string {
	msg := strings.TrimPrefix(err.Error(), migration.ErrValidation.Error()+": ")
	switch {
	case strings.Contains(msg, "transaction control"):
		return msgTxControl
	case msg == migration.ErrValidation.Error():
		return "SQL must not be empty."
	default:
		return "Invalid SQL: " + msg + "."
	}
}

// Posisi PgError relatif terhadap SQL yang dikirim.
func pgErrorLine(err error, sql string, prefixLen int) int {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Position == 0 {
		return 0
	}
	return sqlLineAt(sql, int(pgErr.Position)-prefixLen)
}

// Pesan untuk SQL yang tidak bisa di-EXPLAIN, kosong bila boleh.
func explainPrecheck(sql string) (string, int) {
	stmts, err := migration.Statements(sql)
	if err != nil {
		return "Invalid SQL: " + err.Error() + ".", 0
	}
	if err := migration.RejectTxControl(stmts); err != nil {
		return msgTxControl, txControlLine(sql)
	}
	if len(stmts) != 1 {
		return fmt.Sprintf("EXPLAIN works on one statement at a time. This SQL has %d statements.", len(stmts)), 0
	}
	if !migration.IsExplainable(stmts[0]) {
		return "EXPLAIN only works on a single SELECT, INSERT, UPDATE, DELETE, MERGE, VALUES, or WITH statement. It does not apply to DDL.", 0
	}
	return "", 0
}

func explainFailure(err error, sql string) (*explainView, int) {
	return &explainView{Error: "EXPLAIN failed: " + target.ErrorText(err)}, pgErrorLine(err, sql, len(explainPrefix))
}

func (s *Server) renderDraftNew(w http.ResponseWriter, r *http.Request, status int, v draftNewView) {
	s.render(w, r, status, "migration/draft_new.html", s.dbPageData(r, "New draft", navDBDrafts, v))
}

func (s *Server) createDraft(r *http.Request, in draftInput) (domain.MigrationDraft, error) {
	actor := currentUser(r).ID
	return s.store.CreateMigrationDraft(r.Context(), domain.MigrationDraft{
		DBID: currentDB(r).ID, Title: in.Title, SQLText: in.SQL, CreatedBy: &actor, UpdatedBy: &actor,
	})
}

func draftURL(r *http.Request, id uuid.UUID) string {
	return "/databases/" + currentDB(r).ID.String() + "/migrations/drafts/" + id.String()
}

func (s *Server) handleDraftCreate(w http.ResponseWriter, r *http.Request) {
	in := readDraftInput(r)
	if errs := s.validateDraft(in); len(errs) > 0 {
		s.renderDraftNew(w, r, http.StatusUnprocessableEntity, draftNewView{Title: in.Title, SQL: in.SQL, Form: formState{Errors: errs}})
		return
	}
	d, err := s.createDraft(r, in)
	if err != nil {
		s.serverError(w, r, "create draft", err)
		return
	}
	s.redirectFlash(w, r, draftURL(r, d.ID), "success", "Draft saved.")
}

func (s *Server) handleDraftNewExplain(w http.ResponseWriter, r *http.Request) {
	in := readDraftInput(r)
	v := draftNewView{Title: in.Title, SQL: in.SQL}
	if errs := s.validateDraft(in); len(errs) > 0 {
		v.Form = formState{Errors: errs}
		s.renderDraftNew(w, r, http.StatusUnprocessableEntity, v)
		return
	}
	if msg, line := explainPrecheck(in.SQL); msg != "" {
		v.Explain, v.SQLErrorLine = &explainView{Error: msg}, line
	} else if plan, err := s.targets.Explain(r.Context(), *currentDB(r), in.SQL, s.cfg.MigrationTimeout); err != nil {
		v.Explain, v.SQLErrorLine = explainFailure(err, in.SQL)
	} else {
		v.Explain = &explainView{Plan: plan}
	}
	s.renderDraftNew(w, r, http.StatusOK, v)
}

func (s *Server) handleDraftNewExecute(w http.ResponseWriter, r *http.Request) {
	in := readDraftInput(r)
	errs := s.validateDraft(in)
	if r.FormValue("confirm") != "yes" {
		errs["form"] = "Confirm that you have reviewed the SQL before executing it."
	}
	if len(errs) > 0 {
		s.renderDraftNew(w, r, http.StatusUnprocessableEntity, draftNewView{Title: in.Title, SQL: in.SQL, Form: formState{Errors: errs}})
		return
	}
	d, err := s.createDraft(r, in)
	if err != nil {
		s.serverError(w, r, "create draft", err)
		return
	}
	in.Token = d.Revision
	s.executeDraft(w, r, d, in)
}

func (s *Server) loadDraft(w http.ResponseWriter, r *http.Request) (domain.MigrationDraft, bool) {
	id, ok := urlID(r, "draftID")
	if !ok {
		s.renderError(w, r, http.StatusNotFound, "Draft not found.")
		return domain.MigrationDraft{}, false
	}
	d, err := s.store.GetMigrationDraft(r.Context(), currentDB(r).ID, id)
	if errors.Is(err, store.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "Draft not found. It may have been executed or deleted.")
		return domain.MigrationDraft{}, false
	}
	if err != nil {
		s.serverError(w, r, "get draft", err)
		return domain.MigrationDraft{}, false
	}
	return d, true
}

func (s *Server) renderDraftEditor(w http.ResponseWriter, r *http.Request, status int, v draftEditorView) {
	s.render(w, r, status, "migration/draft_editor.html", s.dbPageData(r, "Drafts", navDBDrafts, v))
}

// Isi editor dengan input user dan token yang dia kirim.
func editorWithInput(d domain.MigrationDraft, in draftInput) draftEditorView {
	row := toDraftRow(d)
	row.Title = in.Title
	if in.Token > 0 {
		row.Revision = in.Token
	}
	return draftEditorView{Draft: row, SQL: in.SQL}
}

func (s *Server) handleDraftEditor(w http.ResponseWriter, r *http.Request) {
	if d, ok := s.loadDraft(w, r); ok {
		s.renderDraftEditor(w, r, http.StatusOK, draftEditorView{Draft: toDraftRow(d), SQL: d.SQLText})
	}
}

func (s *Server) handleDraftSave(w http.ResponseWriter, r *http.Request) {
	d, ok := s.loadDraft(w, r)
	if !ok {
		return
	}
	in := readDraftInput(r)
	if errs := s.validateDraft(in); len(errs) > 0 {
		v := editorWithInput(d, in)
		v.Form = formState{Errors: errs}
		s.renderDraftEditor(w, r, http.StatusUnprocessableEntity, v)
		return
	}
	saved, err := s.store.UpdateMigrationDraft(r.Context(), d.DBID, d.ID, in.Title, in.SQL, in.Token, currentUser(r).ID)
	switch {
	case errors.Is(err, store.ErrConflict):
		v := editorWithInput(d, in)
		v.Form = formState{Errors: map[string]string{"form": msgDraftConflict}}
		s.renderDraftEditor(w, r, http.StatusConflict, v)
		return
	case errors.Is(err, store.ErrNotFound):
		s.renderError(w, r, http.StatusNotFound, "Draft not found. It may have been executed or deleted.")
		return
	case err != nil:
		s.serverError(w, r, "save draft", err)
		return
	}
	s.redirectFlash(w, r, draftURL(r, saved.ID), "success", fmt.Sprintf("Draft saved as revision %d.", saved.Revision))
}

func (s *Server) handleDraftDelete(w http.ResponseWriter, r *http.Request) {
	d, ok := s.loadDraft(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteMigrationDraft(r.Context(), d.DBID, d.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, r, "delete draft", err)
		return
	}
	s.redirectFlash(w, r, "/databases/"+d.DBID.String()+"/migrations/drafts", "success", "Draft deleted.")
}

func (s *Server) handleDraftExplain(w http.ResponseWriter, r *http.Request) {
	d, ok := s.loadDraft(w, r)
	if !ok {
		return
	}
	in := readDraftInput(r)
	v := editorWithInput(d, in)
	if errs := s.validateDraft(in); len(errs) > 0 {
		v.Form = formState{Errors: errs}
		s.renderDraftEditor(w, r, http.StatusUnprocessableEntity, v)
		return
	}
	if msg, line := explainPrecheck(in.SQL); msg != "" {
		v.Explain, v.SQLErrorLine = &explainView{Error: msg}, line
		s.renderDraftEditor(w, r, http.StatusOK, v)
		return
	}
	plan, err := s.migrations.ExplainDraft(r.Context(), migration.ExplainInput{
		Actor: *currentUser(r), DB: *currentDB(r), Level: currentLevel(r),
		DraftID: d.ID, Token: in.Token, SQL: in.SQL,
	})
	switch {
	case err == nil:
		v.Explain = &explainView{Plan: plan}
	case errors.Is(err, migration.ErrConflict):
		v.Form = formState{Errors: map[string]string{"form": msgDraftConflict}}
		s.renderDraftEditor(w, r, http.StatusConflict, v)
		return
	case errors.Is(err, migration.ErrNotFound):
		s.renderError(w, r, http.StatusNotFound, "Draft not found. It may have been executed or deleted.")
		return
	case errors.Is(err, migration.ErrValidation):
		v.Explain = &explainView{Error: validationMessage(err)}
	case errors.Is(err, migration.ErrTarget):
		v.Explain, v.SQLErrorLine = explainFailure(err, in.SQL)
	default:
		s.serverError(w, r, "explain draft", err)
		return
	}
	s.renderDraftEditor(w, r, http.StatusOK, v)
}

func (s *Server) handleDraftExecute(w http.ResponseWriter, r *http.Request) {
	d, ok := s.loadDraft(w, r)
	if !ok {
		return
	}
	in := readDraftInput(r)
	errs := s.validateDraft(in)
	if r.FormValue("confirm") != "yes" {
		errs["form"] = "Confirm that you have reviewed the SQL before executing it."
	}
	if len(errs) > 0 {
		v := editorWithInput(d, in)
		v.Form = formState{Errors: errs}
		s.renderDraftEditor(w, r, http.StatusUnprocessableEntity, v)
		return
	}
	s.executeDraft(w, r, d, in)
}

func (s *Server) executeDraft(w http.ResponseWriter, r *http.Request, d domain.MigrationDraft, in draftInput) {
	h, err := s.migrations.ExecuteDraft(r.Context(), migration.ExecuteInput{
		Actor: *currentUser(r), DB: *currentDB(r), Level: currentLevel(r),
		DraftID: d.ID, Token: in.Token, Title: in.Title, SQL: in.SQL,
	})
	if err == nil {
		s.redirectFlash(w, r, fmt.Sprintf("/databases/%s/migrations?migration=%s", d.DBID, h.ID), "success",
			fmt.Sprintf("Migration v%d executed and committed.", h.Version))
		return
	}

	switch {
	case errors.Is(err, migration.ErrNotFound):
		s.renderError(w, r, http.StatusNotFound, "Draft not found. It may have been executed or deleted.")
		return
	case errors.Is(err, migration.ErrConflict):
		v := editorWithInput(d, in)
		v.Form = formState{Errors: map[string]string{"form": msgDraftConflict}}
		s.renderDraftEditor(w, r, http.StatusConflict, v)
		return
	case errors.Is(err, migration.ErrForbidden):
		s.renderError(w, r, http.StatusForbidden, "You do not have sufficient access to this database.")
		return
	}

	// Draft mungkin sudah tersimpan dengan revisi baru sebelum gagal.
	current, loadErr := s.store.GetMigrationDraft(r.Context(), d.DBID, d.ID)
	if loadErr != nil {
		s.serverError(w, r, "reload draft", loadErr)
		return
	}
	v := draftEditorView{Draft: toDraftRow(current), SQL: in.SQL}
	v.Draft.Title = in.Title

	switch {
	case errors.Is(err, migration.ErrValidation):
		msg := validationMessage(err)
		if msg == msgTxControl {
			v.SQLErrorLine = txControlLine(in.SQL)
		}
		v.Form = formState{Errors: map[string]string{"sql": msg}}
		s.renderDraftEditor(w, r, http.StatusUnprocessableEntity, v)
	case errors.Is(err, migration.ErrBusy), errors.Is(err, migration.ErrLockTimeout):
		v.Form = formState{Errors: map[string]string{"form": "Another migration is running on this database. Your draft was saved. Try again shortly."}}
		s.renderDraftEditor(w, r, http.StatusConflict, v)
	case errors.Is(err, migration.ErrTarget):
		failure := toHistoryRow(h)
		v.Failure = &failure
		v.SQLErrorLine = pgErrorLine(err, in.SQL, 0)
		s.renderDraftEditor(w, r, http.StatusUnprocessableEntity, v)
	default:
		s.serverError(w, r, "execute draft", err)
	}
}
