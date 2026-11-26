package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"leafygo/internal/access"
	"leafygo/internal/auth"
	"leafygo/internal/domain"
	"leafygo/internal/store"
)

const (
	ErrCodeUnauthorized            = "UNAUTHORIZED"
	ErrCodeForbidden               = "FORBIDDEN"
	ErrCodeForbiddenNotGroupLeader = "FORBIDDEN_NOT_GROUP_LEADER"
	ErrCodeInvalidCSRF             = "INVALID_CSRF_TOKEN"
	ErrCodeNotFound                = "NOT_FOUND"
	ErrCodeConflict                = "CONFLICT"
	ErrCodeValidation              = "VALIDATION_ERROR"
	ErrCodeRateLimited             = "RATE_LIMITED"
	ErrCodeInternal                = "INTERNAL_ERROR"

	ErrCodeInvalidMigrationSQL     = "INVALID_MIGRATION_SQL"
	ErrCodeNonTransactionalStmt    = "NON_TRANSACTIONAL_STATEMENT"
	ErrCodeInvalidBatchIdentifier  = "INVALID_BATCH_IDENTIFIER"
	ErrCodeInvalidBatchCursorType  = "INVALID_BATCH_CURSOR_TYPE"
	ErrCodeDryRunBusy              = "DRY_RUN_BUSY"
	ErrCodeDuplicateActiveProposal = "DUPLICATE_ACTIVE_PROPOSAL"
	ErrCodePredecessorNotReady     = "PREDECESSOR_NOT_READY"
	ErrCodePredecessorFailed       = "PREDECESSOR_FAILED"
	ErrCodeApproverSetChanged      = "APPROVER_SET_CHANGED"
	ErrCodeNoAccessGroups          = "NO_ACCESS_GROUPS"
	ErrCodeMissingGroupLeader      = "MISSING_GROUP_LEADER"
	ErrCodeMigrationNotSupersedable = "MIGRATION_NOT_SUPERSEDABLE"
	ErrCodeMigrationImmutable      = "MIGRATION_IMMUTABLE"
	ErrCodeUserHasHistory          = "USER_HAS_HISTORY"
	ErrCodeGroupHasHistory         = "GROUP_HAS_HISTORY"
	ErrCodeDBHasActiveWork         = "DB_HAS_ACTIVE_WORK"
)

type apiErrorEnvelope struct {
	Error apiErrorDetail `json:"error"`
}

type apiErrorDetail struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	CorrelationID string `json:"correlation_id"`
	TraceID       string `json:"trace_id"`
}

func correlationIDFromRequest(r *http.Request) string {
	if cid := strings.TrimSpace(r.Header.Get("X-Correlation-ID")); cid != "" {
		return cid
	}
	if reqID := middleware.GetReqID(r.Context()); reqID != "" {
		return "req-" + reqID
	}
	return "req-" + uuid.NewString()
}

func traceIDFromRequest(r *http.Request) string {
	if tp := strings.TrimSpace(r.Header.Get("traceparent")); tp != "" {
		parts := strings.Split(tp, "-")
		if len(parts) >= 2 && len(parts[1]) == 32 {
			return parts[1]
		}
	}
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeAPIError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, status, apiErrorEnvelope{
		Error: apiErrorDetail{
			Code:          code,
			Message:       message,
			CorrelationID: correlationIDFromRequest(r),
			TraceID:       traceIDFromRequest(r),
		},
	})
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		writeAPIError(w, r, http.StatusBadRequest, ErrCodeValidation, "Request body is required.")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4*(1<<20))
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			writeAPIError(w, r, http.StatusBadRequest, ErrCodeValidation, "Request body cannot be empty.")
			return false
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeAPIError(w, r, http.StatusRequestEntityTooLarge, ErrCodeValidation, "Request body is too large.")
			return false
		}
		writeAPIError(w, r, http.StatusBadRequest, ErrCodeValidation, "Invalid JSON payload.")
		return false
	}
	return true
}

func (s *Server) apiAuthenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("sid")
		if err != nil || cookie.Value == "" {
			writeAPIError(w, r, http.StatusUnauthorized, ErrCodeUnauthorized, "Authentication required.")
			return
		}
		sess, newRaw, err := s.sessions.Resolve(r.Context(), cookie.Value)
		if err != nil {
			s.clearSessionCookie(w)
			writeAPIError(w, r, http.StatusUnauthorized, ErrCodeUnauthorized, "Invalid or expired session.")
			return
		}
		if newRaw != "" {
			s.setSessionCookie(w, newRaw)
		}
		user, err := s.store.GetUserByID(r.Context(), sess.UserID)
		if err != nil || !user.IsActive {
			s.clearSessionCookie(w)
			writeAPIError(w, r, http.StatusUnauthorized, ErrCodeUnauthorized, "Account is inactive or not found.")
			return
		}
		leaderGroups, err := s.store.ListUserLeaderGroupIDs(r.Context(), user.ID)
		if err == nil {
			user.LeaderOfGroups = leaderGroups
		}
		ctx := context.WithValue(r.Context(), sessionKey, &sess)
		ctx = context.WithValue(ctx, userKey, &user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) apiCSRFProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		sess := currentSession(r)
		if sess == nil {
			writeAPIError(w, r, http.StatusForbidden, ErrCodeInvalidCSRF, "Invalid session.")
			return
		}
		token := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
		if token == "" {
			token = strings.TrimSpace(r.URL.Query().Get("csrf_token"))
		}
		if !constantTimeEqual(token, sess.CSRFToken) {
			writeAPIError(w, r, http.StatusForbidden, ErrCodeInvalidCSRF, "Invalid or missing X-CSRF-Token header.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) apiRequirePlatformAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := currentUser(r)
		if user == nil || !user.IsPlatformAdmin {
			s.auditDenied(r, "admin", r.URL.Path)
			writeAPIError(w, r, http.StatusForbidden, ErrCodeForbidden, "This endpoint is restricted to Platform Admins.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) apiRequireDBAction(action access.Action) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := currentUser(r)
			if user == nil {
				writeAPIError(w, r, http.StatusUnauthorized, ErrCodeUnauthorized, "Authentication required.")
				return
			}
			id, err := uuid.Parse(chi.URLParam(r, "id"))
			if err != nil {
				writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Database not found.")
				return
			}
			conn, err := s.store.GetDBConnection(r.Context(), id)
			if errors.Is(err, store.ErrNotFound) {
				writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Database not found.")
				return
			}
			if err != nil {
				s.logger.Error("load database", "error", err)
				writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Could not load the database.")
				return
			}
			level, err := s.policy.Level(r.Context(), *user, id)
			if err != nil {
				s.logger.Error("check access", "error", err)
				writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Could not check access rights.")
				return
			}
			if !access.Can(level, action) {
				s.auditDenied(r, string(action), id.String())
				if user.IsPlatformAdmin {
					writeAPIError(w, r, http.StatusForbidden, ErrCodeForbidden, "Platform Admin accounts cannot access database tools.")
				} else {
					writeAPIError(w, r, http.StatusForbidden, ErrCodeForbidden, "You do not have sufficient access to this database.")
				}
				return
			}
			ctx := context.WithValue(r.Context(), dbKey, &conn)
			ctx = context.WithValue(ctx, levelKey, level)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

type apiAuthUserResponse struct {
	ID              uuid.UUID   `json:"id"`
	Username        string      `json:"username"`
	IsPlatformAdmin bool        `json:"is_platform_admin"`
	IsAdmin         bool        `json:"is_admin"`
	IsActive        bool        `json:"is_active"`
	CSRFToken       string      `json:"csrf_token"`
	LeaderOfGroups  []uuid.UUID `json:"leader_of_groups"`
}

func buildAuthUserResponse(u domain.User, csrfToken string) apiAuthUserResponse {
	leaders := u.LeaderOfGroups
	if leaders == nil {
		leaders = []uuid.UUID{}
	}
	return apiAuthUserResponse{
		ID:              u.ID,
		Username:        u.Username,
		IsPlatformAdmin: u.IsPlatformAdmin,
		IsAdmin:         u.IsPlatformAdmin,
		IsActive:        u.IsActive,
		CSRFToken:       csrfToken,
		LeaderOfGroups:  leaders,
	}
}

func (s *Server) handleAPIAuthLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/x-www-form-urlencoded") {
		if err := r.ParseForm(); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, ErrCodeValidation, "Invalid form payload.")
			return
		}
		req.Username = r.FormValue("username")
		req.Password = r.FormValue("password")
	} else {
		if !decodeJSONBody(w, r, &req) {
			return
		}
	}

	username := strings.TrimSpace(req.Username)
	password := req.Password
	if username == "" || password == "" {
		writeAPIError(w, r, http.StatusUnprocessableEntity, ErrCodeValidation, "Username and password are required.")
		return
	}

	key := clientIP(r) + "|" + strings.ToLower(username)
	if !s.limiter.allowed(key) {
		writeAPIError(w, r, http.StatusTooManyRequests, ErrCodeRateLimited, "Too many sign-in attempts. Try again in a few minutes.")
		return
	}

	user, err := s.store.GetUserByUsername(r.Context(), username)
	if errors.Is(err, store.ErrNotFound) {
		auth.PerformDummyVerify(password)
		s.limiter.fail(key)
		writeAPIError(w, r, http.StatusUnauthorized, ErrCodeUnauthorized, "Incorrect username or password.")
		return
	}
	if err != nil {
		s.logger.Error("api login lookup", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Could not process sign-in.")
		return
	}
	ok, err := auth.VerifyPassword(user.PasswordHash, password)
	if err != nil || !ok {
		s.limiter.fail(key)
		writeAPIError(w, r, http.StatusUnauthorized, ErrCodeUnauthorized, "Incorrect username or password.")
		return
	}
	s.limiter.reset(key)
	if !user.IsActive {
		writeAPIError(w, r, http.StatusForbidden, ErrCodeForbidden, "This account is inactive. Contact a Platform Admin.")
		return
	}

	if old, err := r.Cookie("sid"); err == nil && old.Value != "" {
		_ = s.sessions.Revoke(r.Context(), old.Value)
	}
	raw, sess, err := s.sessions.Create(r.Context(), user.ID)
	if err != nil {
		s.logger.Error("api create session", "error", err)
		writeAPIError(w, r, http.StatusInternalServerError, ErrCodeInternal, "Could not create a session.")
		return
	}
	s.setSessionCookie(w, raw)

	leaderGroups, err := s.store.ListUserLeaderGroupIDs(r.Context(), user.ID)
	if err == nil {
		user.LeaderOfGroups = leaderGroups
	}

	writeJSON(w, http.StatusOK, buildAuthUserResponse(user, sess.CSRFToken))
}

func (s *Server) handleAPIAuthLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("sid"); err == nil && cookie.Value != "" {
		if err := s.sessions.Revoke(r.Context(), cookie.Value); err != nil {
			s.logger.Error("api revoke session", "error", err)
		}
	}
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (s *Server) handleAPIAuthMe(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	sess := currentSession(r)
	if user == nil || sess == nil {
		writeAPIError(w, r, http.StatusUnauthorized, ErrCodeUnauthorized, "Authentication required.")
		return
	}
	writeJSON(w, http.StatusOK, buildAuthUserResponse(*user, sess.CSRFToken))
}

