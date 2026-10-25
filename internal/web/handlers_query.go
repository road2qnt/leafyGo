package web

import (
	"errors"
	"net/http"
	"strings"

	"leafygo/internal/queryconsole"
)

func (s *Server) queryBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		maxBytes := 4*s.cfg.MaxQueryBytes + 64*1024
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleQueryConsoleGet(w http.ResponseWriter, r *http.Request) {
	v := queryConsoleView{}
	s.render(w, r, http.StatusOK, "query/console.html", s.dbPageData(r, "SQL Console", navDBQuery, v))
}

func (s *Server) handleQueryConsolePost(w http.ResponseWriter, r *http.Request) {
	sql := strings.ReplaceAll(r.FormValue("sql"), "\r\n", "\n")
	user := currentUser(r)
	db := currentDB(r)
	level := currentLevel(r)

	in := queryconsole.ExecuteInput{
		Actor: *user,
		DB:    *db,
		Level: level,
		SQL:   sql,
	}

	res, err := s.queries.Execute(r.Context(), in)
	v := queryConsoleView{
		SQL: sql,
	}

	if err != nil {
		status := http.StatusUnprocessableEntity
		var tgtErr *queryconsole.TargetError
		switch {
		case errors.Is(err, queryconsole.ErrForbidden):
			status = http.StatusForbidden
			v.Error = "You do not have access to run queries on this database."
		case errors.Is(err, queryconsole.ErrBusy):
			status = http.StatusConflict
			v.Error = "All query execution slots are currently in use. Please wait a moment and try again."
		case errors.Is(err, queryconsole.ErrTimeout):
			status = http.StatusGatewayTimeout
			v.Error = "Query execution timed out."
		case errors.As(err, &tgtErr):
			if tgtErr.Code == "55P03" {
				status = http.StatusConflict
				v.Error = tgtErr.Message
				v.SQLState = tgtErr.Code
			} else if tgtErr.Code == "57014" {
				status = http.StatusGatewayTimeout
				v.Error = "Query execution timed out."
				v.SQLState = tgtErr.Code
			} else {
				status = http.StatusUnprocessableEntity
				v.Error = tgtErr.Message
				v.SQLState = tgtErr.Code
				v.Position = tgtErr.Position
				v.IsWriteRejection = tgtErr.IsWriteRejection
				if tgtErr.Position > 0 {
					v.SQLErrorLine = sqlLineAt(sql, tgtErr.Position)
				}
			}
		default:
			status = http.StatusUnprocessableEntity
			v.Error = err.Error()
		}

		s.render(w, r, status, "query/console.html", s.dbPageData(r, "SQL Console", navDBQuery, v))
		return
	}

	v.Result = &res
	if res.Truncated {
		v.Notice = "Result set has been truncated to avoid exceeding display limits."
	}
	s.render(w, r, http.StatusOK, "query/console.html", s.dbPageData(r, "SQL Console", navDBQuery, v))
}

func (s *Server) handleQueryToDraft(w http.ResponseWriter, r *http.Request) {
	sql := strings.ReplaceAll(r.FormValue("sql"), "\r\n", "\n")
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = "Dari SQL Console"
	}

	in := draftInput{
		Title: title,
		SQL:   sql,
	}

	if errs := s.validateDraft(in); len(errs) > 0 {
		msg := "Could not create draft"
		if errs["sql"] != "" {
			msg += ": " + errs["sql"]
		} else if errs["title"] != "" {
			msg += ": " + errs["title"]
		}
		s.renderError(w, r, http.StatusUnprocessableEntity, msg)
		return
	}

	d, err := s.createDraft(r, in)
	if err != nil {
		s.serverError(w, r, "create draft from query", err)
		return
	}

	http.Redirect(w, r, draftURL(r, d.ID), http.StatusSeeOther)
}
