package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"

	"leafygo/internal/introspect"
	"leafygo/internal/target"
)

func (s *Server) dbPageData(r *http.Request, title, nav string, data any) pageData {
	db := currentDB(r)
	return pageData{Title: title + " · " + db.Name, Nav: nav, DB: db, Level: currentLevel(r), TargetDown: !s.targetReachable(r), Data: data}
}

func (s *Server) checkTarget(r *http.Request) error {
	pool, err := s.targetPool(r)
	if err != nil {
		return err
	}
	return pool.Ping(r.Context())
}

func (s *Server) targetReachable(r *http.Request) bool {
	return s.checkTarget(r) == nil
}

func targetErrorText(err error) string {
	return "The database cannot be reached: " + target.ErrorText(err)
}

func (s *Server) targetPool(r *http.Request) (*pgxpool.Pool, error) {
	return s.targets.Pool(r.Context(), *currentDB(r))
}

func (s *Server) logTargetError(r *http.Request, err error) {
	s.logger.Warn("target query failed", "db_id", currentDB(r).ID.String(), "error", target.ErrorText(err))
}

func toTableInfo(t introspect.Table) tableInfo {
	return tableInfo{Schema: t.Schema, Name: t.Name, Kind: t.Kind, RowCount: t.RowCount, CanSelect: t.CanSelect}
}

func (s *Server) listTables(r *http.Request) ([]tableInfo, error) {
	pool, err := s.targetPool(r)
	if err != nil {
		return nil, err
	}
	tables, err := introspect.ListTables(r.Context(), pool)
	if err != nil {
		return nil, err
	}
	out := make([]tableInfo, len(tables))
	for i, t := range tables {
		out[i] = toTableInfo(t)
	}
	return out, nil
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	db := currentDB(r)
	v := overviewView{DB: toDBView(*db)}
	if tables, err := s.listTables(r); err != nil {
		s.logTargetError(r, err)
		v.TargetError = targetErrorText(err)
	} else {
		v.TableCount = len(tables)
	}
	latest, err := s.store.ListMigrationHistory(r.Context(), db.ID, 1)
	if err != nil {
		s.serverError(w, r, "latest migration", err)
		return
	}
	if len(latest) > 0 {
		row := toHistoryRow(latest[0])
		v.LatestMigration = &row
	}
	s.render(w, r, http.StatusOK, "viewer/overview.html", s.dbPageData(r, "Overview", navDBOverview, v))
}

func toTableDetail(d introspect.TableDetail) *tableDetail {
	out := &tableDetail{tableInfo: toTableInfo(d.Table), PrimaryKey: d.PrimaryKey}
	for _, c := range d.Columns {
		out.Columns = append(out.Columns, columnInfo(c))
	}
	for _, c := range d.Constraints {
		out.Constraints = append(out.Constraints, constraintInfo(c))
	}
	for _, ix := range d.Indexes {
		out.Indexes = append(out.Indexes, indexInfo(ix))
	}
	return out
}

func (s *Server) handleSchema(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	schema, table := q.Get("schema"), q.Get("table")
	if schema == "" || table == "" {
		tables, err := s.listTables(r)
		if err != nil {
			s.renderSchemaDown(w, r, err)
			return
		}
		s.render(w, r, http.StatusOK, "viewer/schema.html", s.dbPageData(r, "Schema", navDBSchema, schemaView{Tables: tables}))
		return
	}
	pool, err := s.targetPool(r)
	if err != nil {
		s.renderSchemaDown(w, r, err)
		return
	}
	d, err := introspect.Describe(r.Context(), pool, schema, table)
	if errors.Is(err, introspect.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "Table "+schema+"."+table+" not found.")
		return
	}
	if err != nil {
		s.renderSchemaDown(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "viewer/schema.html", s.dbPageData(r, "Schema", navDBSchema, schemaView{Detail: toTableDetail(d)}))
}

func (s *Server) renderSchemaDown(w http.ResponseWriter, r *http.Request, err error) {
	s.logTargetError(r, err)
	s.render(w, r, http.StatusOK, "viewer/schema.html", s.dbPageData(r, "Schema", navDBSchema, schemaView{TargetError: targetErrorText(err)}))
}

func (s *Server) renderDataDown(w http.ResponseWriter, r *http.Request, err error) {
	s.logTargetError(r, err)
	s.render(w, r, http.StatusOK, "viewer/data.html", s.dbPageData(r, "Data", navDBData, dataView{TargetError: targetErrorText(err)}))
}

func queryInt(r *http.Request, key string, fallback int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

func (s *Server) handleData(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	schema, table := q.Get("schema"), q.Get("table")
	renderPick := func(notice string) {
		tables, err := s.listTables(r)
		if err != nil {
			s.renderDataDown(w, r, err)
			return
		}
		s.render(w, r, http.StatusOK, "viewer/data.html", s.dbPageData(r, "Data", navDBData, dataView{Tables: tables, Notice: notice}))
	}
	if schema == "" || table == "" {
		renderPick("")
		return
	}

	pool, err := s.targetPool(r)
	if err != nil {
		s.renderDataDown(w, r, err)
		return
	}
	d, err := introspect.Describe(r.Context(), pool, schema, table)
	if errors.Is(err, introspect.ErrNotFound) {
		s.renderError(w, r, http.StatusNotFound, "Table "+schema+"."+table+" not found.")
		return
	}
	if err != nil {
		s.renderDataDown(w, r, err)
		return
	}
	if !d.CanSelect {
		renderPick("The database credentials have no SELECT permission on " + schema + "." + table + ".")
		return
	}

	page := queryInt(r, "page", 1)
	size := min(queryInt(r, "limit", s.cfg.ViewerPageSize), s.cfg.ViewerMaxPageSize)
	p, err := introspect.ReadPage(r.Context(), pool, d, page, size)
	if err != nil {
		s.renderDataDown(w, r, err)
		return
	}
	columns := make([]dataColumn, len(p.Columns))
	for i, c := range p.Columns {
		columns[i] = dataColumn(c)
	}
	dp := &dataPage{
		Schema: schema, Table: table, Columns: columns, Page: page, PageSize: size,
		HasNext: p.HasNext, RowCount: d.RowCount,
	}
	for _, row := range p.Rows {
		cells := make([]dataCell, len(row))
		for i, c := range row {
			cells[i] = dataCell(c)
		}
		dp.Rows = append(dp.Rows, cells)
	}
	if !p.Ordered {
		dp.OrderNote = "This " + relkindNoun(d.Kind) + " has no primary key, so row order across pages is not guaranteed to be stable."
	}
	s.render(w, r, http.StatusOK, "viewer/data.html", s.dbPageData(r, "Data", navDBData, dataView{Page: dp}))
}

func relkindNoun(kind string) string {
	switch kind {
	case "v":
		return "view"
	case "m":
		return "materialized view"
	default:
		return "table"
	}
}
