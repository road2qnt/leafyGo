package web

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

type apiError struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	CorrelationID string `json:"correlation_id,omitempty"`
	TraceID       string `json:"trace_id,omitempty"`
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.logger.Error("encode json response", "error", err)
	}
}

func (s *Server) writeAPIError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	s.writeJSON(w, status, map[string]apiError{"error": {
		Code:          code,
		Message:       message,
		CorrelationID: middleware.GetReqID(r.Context()),
	}})
}
