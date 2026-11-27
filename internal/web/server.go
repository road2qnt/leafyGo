package web

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"leafygo/internal/access"
	"leafygo/internal/auth"
	"leafygo/internal/config"
	"leafygo/internal/domain"
	"leafygo/internal/migration"
	"leafygo/internal/queryconsole"
	"leafygo/internal/secret"
	"leafygo/internal/store"
	"leafygo/internal/target"
	"leafygo/internal/telemetry"
)

//go:embed templates/*.html templates/pages/*/*.html templates/partials/*.html static/*
var assets embed.FS

//go:embed all:dist
var spaAssets embed.FS

type Server struct {
	cfg        config.Config
	store      *store.Store
	sessions   *auth.Manager
	policy     *access.Policy
	logger     *slog.Logger
	limiter    *loginLimiter
	box        *secret.Box
	targets    *target.Manager
	migrations *migration.Service
	queries    *queryconsole.Service

	metricsHandler http.Handler
}

// cfg.EncryptionKey sudah divalidasi config.Load.
func New(cfg config.Config, st *store.Store, sessions *auth.Manager, policy *access.Policy, logger *slog.Logger) *Server {
	if cfg.ViewerPageSize <= 0 {
		cfg.ViewerPageSize = 50
	}
	if cfg.ViewerMaxPageSize < cfg.ViewerPageSize {
		cfg.ViewerMaxPageSize = max(cfg.ViewerPageSize, 100)
	}
	if cfg.MaxMigrationBytes <= 0 {
		cfg.MaxMigrationBytes = 1 << 20
	}
	box, err := secret.New(cfg.EncryptionKey)
	if err != nil {
		panic(err)
	}
	targets := target.New(box, target.Options{
		ConnectTimeout: cfg.TargetConnectTimeout,
		ViewerTimeout:  cfg.ViewerStatementTimeout,
	})
	queryRunner := queryconsole.NewRunner(box, cfg.TargetConnectTimeout, "leafygo")
	queries := queryconsole.NewService(queryRunner, logger, queryconsole.ServiceOptions{
		MaxConcurrent:       cfg.QueryMaxConcurrent,
		AdvisoryLockTimeout: cfg.MigrationAdvisoryLockTimeout,
		QueryTimeout:        cfg.QueryTimeout,
		MaxQueryBytes:       cfg.MaxQueryBytes,
		MaxRows:             cfg.QueryMaxRows,
		MaxCellBytes:        cfg.QueryMaxCellBytes,
		MaxResultBytes:      cfg.QueryMaxResultBytes,
	})
	return &Server{
		cfg:      cfg,
		store:    st,
		sessions: sessions,
		policy:   policy,
		logger:   logger,
		limiter:  newLoginLimiter(8, 5*time.Minute),
		box:      box,
		targets:  targets,
		migrations: migration.NewService(st, targets, migration.Options{
			AdvisoryLockTimeout: cfg.MigrationAdvisoryLockTimeout,
			MigrationTimeout:    cfg.MigrationTimeout,
			MaxMigrationBytes:   int(cfg.MaxMigrationBytes),
		}),
		queries: queries,
	}
}

func (s *Server) Close() { s.targets.Close() }

// TargetPools exposes live target pools for Prometheus collection.
func (s *Server) TargetPools() []target.PoolSnapshot {
	if s.targets == nil {
		return nil
	}
	return s.targets.Snapshots()
}

// SetMetricsHandler mounts the Prometheus scrape endpoint. The handler is
// public (outside the session guard) so Prometheus can scrape it.
func (s *Server) SetMetricsHandler(handler http.Handler) {
	s.metricsHandler = handler
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		// CodeMirror and React set inline style attributes; scripts stay strict.
		if strings.HasPrefix(r.URL.Path, "/app") {
			h.Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; img-src 'self' data:; font-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'")
		} else {
			h.Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	// Telemetry wraps Recoverer so a panic still produces a 500 span/log.
	r.Use(telemetryMiddleware(s.logger))
	r.Use(middleware.Recoverer)
	r.Use(s.securityHeaders)

	if s.metricsHandler != nil {
		r.Method(http.MethodGet, "/metrics", s.metricsHandler)
	}

	staticFS, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err)
	}
	staticHandler := http.StripPrefix("/static/", http.FileServer(http.FS(staticFS)))
	r.Handle("/static/*", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		staticHandler.ServeHTTP(w, req)
	}))

	s.mountSPA(r)

	if s.cfg.UIPreview {
		r.Get("/_preview", s.handlePreviewIndex)
		r.Get("/_preview/{key}", s.handlePreview)
	}

	if s.cfg.SPARoot {
		r.Get("/", func(w http.ResponseWriter, req *http.Request) {
			http.Redirect(w, req, "/app/", http.StatusPermanentRedirect)
		})
	}

	r.Get("/login", s.handleLoginGet)
	r.Post("/login", s.handleLoginPost)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/login", s.handleAPIAuthLogin)

		r.Group(func(r chi.Router) {
			r.Use(s.apiAuthenticate)
			r.Use(s.apiCSRFProtect)

			r.Post("/auth/logout", s.handleAPIAuthLogout)
			r.Get("/auth/me", s.handleAPIAuthMe)

			r.Route("/admin", func(r chi.Router) {
				r.Use(s.apiRequirePlatformAdmin)

				r.Get("/users", s.handleAPIAdminUsersList)
				r.Post("/users", s.handleAPIAdminUserCreate)
				r.Post("/users/{userID}/active", s.handleAPIAdminUserActive)
				r.Post("/users/{userID}/password", s.handleAPIAdminUserPassword)
				r.Delete("/users/{userID}", s.handleAPIAdminUserDelete)

				r.Get("/groups", s.handleAPIAdminGroupsList)
				r.Post("/groups", s.handleAPIAdminGroupCreate)
				r.Get("/groups/{groupID}", s.handleAPIAdminGroupGet)
				r.Put("/groups/{groupID}", s.handleAPIAdminGroupUpdate)
				r.Put("/groups/{groupID}/leader", s.handleAPIAdminGroupLeader)
				r.Post("/groups/{groupID}/members", s.handleAPIAdminGroupAddMember)
				r.Delete("/groups/{groupID}/members/{userID}", s.handleAPIAdminGroupRemoveMember)
				r.Post("/groups/{groupID}/access", s.handleAPIAdminGroupAccess)
				r.Delete("/groups/{groupID}", s.handleAPIAdminGroupDelete)

				r.Get("/databases", s.handleAPIAdminDatabasesList)
				r.Post("/databases", s.handleAPIAdminDatabaseCreate)
				r.Get("/databases/{dbID}", s.handleAPIAdminDatabaseGet)
				r.Put("/databases/{dbID}", s.handleAPIAdminDatabaseUpdate)
				r.Post("/databases/{dbID}/test", s.handleAPIAdminDatabaseTest)
				r.Delete("/databases/{dbID}", s.handleAPIAdminDatabaseDelete)
			})

			r.Get("/databases", s.handleAPIDatabasesList)

			r.Route("/databases/{id}", func(r chi.Router) {
				r.With(s.apiRequireDBAction(access.ActionViewOverview)).Get("/overview", s.handleAPIDatabaseOverview)
				r.With(s.apiRequireDBAction(access.ActionViewSchema)).Get("/schema", s.handleAPIDatabaseSchema)
				r.With(s.apiRequireDBAction(access.ActionViewData)).Get("/data", s.handleAPIDatabaseData)
				r.With(s.apiRequireDBAction(access.ActionQueryConsole)).Post("/query", s.handleAPIDatabaseQuery)
				r.With(s.apiRequireDBAction(access.ActionQueryConsole)).Post("/query/explain", s.handleAPIDatabaseQueryExplain)

				r.With(s.apiRequireDBAction(access.ActionViewHistory)).Get("/migrations", s.handleAPIMigrationsList)
				r.With(s.apiRequireDBAction(access.ActionProposeMigration)).Post("/migrations", s.handleAPIMigrationCreate)
				r.With(s.apiRequireDBAction(access.ActionViewHistory)).Get("/migrations/{mid}", s.handleAPIMigrationGet)
				r.With(s.apiRequireDBAction(access.ActionProposeMigration)).Put("/migrations/{mid}", s.handleAPIMigrationUpdate)
				r.With(s.apiRequireDBAction(access.ActionProposeMigration)).Delete("/migrations/{mid}", s.handleAPIMigrationDelete)
				r.With(s.apiRequireDBAction(access.ActionProposeMigration)).Post("/migrations/{mid}/validate", s.handleAPIMigrationValidate)

				r.With(s.apiRequireDBAction(access.ActionViewHistory)).Get("/proposals", s.handleAPIProposalsList)
				r.With(s.apiRequireDBAction(access.ActionProposeMigration)).Post("/proposals", s.handleAPIProposalSubmit)
				r.With(s.apiRequireDBAction(access.ActionViewHistory)).Get("/proposals/{pid}", s.handleAPIProposalGet)
				r.With(s.apiRequireDBAction(access.ActionWithdrawProposal)).Delete("/proposals/{pid}", s.handleAPIProposalDelete)
				r.With(s.apiRequireDBAction(access.ActionWithdrawProposal)).Post("/proposals/{pid}/withdraw", s.handleAPIProposalWithdraw)
				r.With(s.apiRequireDBAction(access.ActionApproveProposal)).Post("/proposals/{pid}/approve", s.handleAPIProposalApprove)
				r.With(s.apiRequireDBAction(access.ActionApproveProposal)).Post("/proposals/{pid}/reject", s.handleAPIProposalReject)

				r.With(s.apiRequireDBAction(access.ActionViewHistory)).Get("/history", s.handleAPIHistoryList)
				r.With(s.apiRequireDBAction(access.ActionViewHistory)).Get("/history/{execID}", s.handleAPIHistoryDetail)

				r.With(s.apiRequireDBAction(access.ActionViewHistory)).Get("/jobs/{jobID}", s.handleAPIJobGet)
			})
		})
	})

	r.Group(func(r chi.Router) {
		r.Use(s.authenticate)
		r.Use(s.csrfProtect)

		r.Post("/logout", s.handleLogout)
		if !s.cfg.SPARoot {
			r.Get("/", s.handleDashboard)
		}

		r.Route("/admin", func(r chi.Router) {
			r.Use(s.requirePlatformAdmin)
			r.Get("/users", s.handleAdminUsers)
			r.Post("/users", s.handleAdminUserCreate)
			r.Post("/users/{userID}/password", s.handleAdminUserPassword)
			r.Post("/users/{userID}/active", s.handleAdminUserActive)

			r.Get("/groups", s.handleAdminGroups)
			r.Get("/groups/new", s.handleAdminGroupNew)
			r.Post("/groups", s.handleAdminGroupCreate)
			r.Get("/groups/{groupID}", s.handleAdminGroup)
			r.Post("/groups/{groupID}", s.handleAdminGroupUpdate)
			r.Post("/groups/{groupID}/members", s.handleAdminGroupMembers)
			r.Post("/groups/{groupID}/access", s.handleAdminGroupAccess)
			r.Post("/groups/{groupID}/delete", s.handleAdminGroupDelete)

			r.Get("/databases", s.handleAdminDatabases)
			r.Get("/databases/new", s.handleAdminDatabaseNew)
			r.Post("/databases", s.handleAdminDatabaseCreate)
			r.Get("/databases/{dbID}", s.handleAdminDatabase)
			r.Post("/databases/{dbID}", s.handleAdminDatabaseUpdate)
			r.Post("/databases/{dbID}/test", s.handleAdminDatabaseTest)
			r.Post("/databases/{dbID}/delete", s.handleAdminDatabaseDelete)
		})

		r.Route("/databases/{id}", func(r chi.Router) {
			r.With(s.requireDBAction(access.ActionViewOverview)).Get("/", s.handleOverview)
			r.With(s.requireDBAction(access.ActionViewSchema)).Get("/schema", s.handleSchema)
			r.With(s.requireDBAction(access.ActionViewData)).Get("/data", s.handleData)
			r.With(s.requireDBAction(access.ActionQueryConsole)).Get("/query", s.handleQueryConsoleGet)
			r.With(s.requireDBAction(access.ActionQueryConsole), s.queryBodyLimit).Post("/query", s.handleQueryConsolePost)
			r.With(s.requireDBAction(access.ActionQueryConsole), s.queryBodyLimit).Post("/query/explain", s.handleQueryConsoleExplain)
			r.With(s.requireDBAction(access.ActionManageDrafts), s.queryBodyLimit).Post("/query/to-draft", s.handleQueryToDraft)
			r.With(s.requireDBAction(access.ActionViewHistory)).Get("/migrations", s.handleHistory)

			r.Group(func(r chi.Router) {
				r.Use(s.requireDBAction(access.ActionManageDrafts))
				r.Get("/migrations/drafts", s.handleDrafts)
				r.Get("/migrations/new", s.handleDraftNew)
				r.Post("/migrations/drafts", s.handleDraftCreate)
				r.Get("/migrations/drafts/{draftID}", s.handleDraftEditor)
				r.Post("/migrations/drafts/{draftID}", s.handleDraftSave)
				r.Post("/migrations/drafts/{draftID}/delete", s.handleDraftDelete)
			})
			r.With(s.requireDBAction(access.ActionExplainMigration)).Post("/migrations/drafts/explain", s.handleDraftNewExplain)
			r.With(s.requireDBAction(access.ActionExplainMigration)).Post("/migrations/drafts/{draftID}/explain", s.handleDraftExplain)
			r.With(s.requireDBAction(access.ActionExecuteMigration)).Post("/migrations/drafts/execute", s.handleDraftNewExecute)
			r.With(s.requireDBAction(access.ActionExecuteMigration)).Post("/migrations/drafts/{draftID}/execute", s.handleDraftExecute)
		})
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeAPIError(w, r, http.StatusNotFound, ErrCodeNotFound, "Endpoint not found.")
			return
		}
		s.renderError(w, r, http.StatusNotFound, "Page not found.")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeAPIError(w, r, http.StatusMethodNotAllowed, ErrCodeValidation, "Request method not allowed.")
			return
		}
		s.renderError(w, r, http.StatusMethodNotAllowed, "Request method not allowed.")
	})

	return r
}

type ctxKey int

const (
	userKey ctxKey = iota
	sessionKey
	dbKey
	levelKey
)

func currentUser(r *http.Request) *domain.User {
	u, _ := r.Context().Value(userKey).(*domain.User)
	return u
}

func currentSession(r *http.Request) *domain.Session {
	sess, _ := r.Context().Value(sessionKey).(*domain.Session)
	return sess
}

func currentDB(r *http.Request) *domain.DBConnection {
	db, _ := r.Context().Value(dbKey).(*domain.DBConnection)
	return db
}

func currentLevel(r *http.Request) domain.AccessLevel {
	level, _ := r.Context().Value(levelKey).(domain.AccessLevel)
	return level
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("sid")
		if err != nil || cookie.Value == "" {
			s.redirectToLogin(w, r)
			return
		}
		sess, newRaw, err := s.sessions.Resolve(r.Context(), cookie.Value)
		if err != nil {
			s.clearSessionCookie(w)
			s.redirectToLogin(w, r)
			return
		}
		if newRaw != "" {
			s.setSessionCookie(w, newRaw)
		}
		user, err := s.store.GetUserByID(r.Context(), sess.UserID)
		if err != nil || !user.IsActive {
			s.clearSessionCookie(w)
			s.redirectToLogin(w, r)
			return
		}
		actor := "user:" + user.Username
		ctx := telemetry.WithActor(r.Context(), actor)
		if info := requestInfoFrom(ctx); info != nil {
			info.Actor = actor
		}
		ctx = context.WithValue(ctx, sessionKey, &sess)
		ctx = context.WithValue(ctx, userKey, &user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) csrfProtect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		sess := currentSession(r)
		if sess == nil {
			s.renderError(w, r, http.StatusForbidden, "Invalid session.")
			return
		}
		if isQueryConsoleRoute(r.URL.Path) {
			maxBytes := 4*s.cfg.MaxQueryBytes + 64*1024
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		if err := r.ParseForm(); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				s.renderError(w, r, http.StatusRequestEntityTooLarge, "Request body is too large.")
				return
			}
			s.renderError(w, r, http.StatusBadRequest, "Invalid form.")
			return
		}
		token := r.FormValue("csrf_token")
		if token == "" {
			token = r.Header.Get("X-CSRF-Token")
		}
		if !constantTimeEqual(token, sess.CSRFToken) {
			s.renderError(w, r, http.StatusForbidden, "Invalid CSRF token. Reload the page and try again.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isQueryConsoleRoute(path string) bool {
	if !strings.HasPrefix(path, "/databases/") {
		return false
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 3 && parts[0] == "databases" && parts[2] == "query" {
		return true
	}
	if len(parts) == 4 && parts[0] == "databases" && parts[2] == "query" && (parts[3] == "to-draft" || parts[3] == "explain") {
		return true
	}
	return false
}

func (s *Server) requirePlatformAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := currentUser(r)
		if user == nil || !user.IsPlatformAdmin {
			s.auditDenied(r, "admin", r.URL.Path)
			s.renderError(w, r, http.StatusForbidden, "This page is for Platform Admins only.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireDBAction(action access.Action) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := currentUser(r)
			if user == nil {
				s.redirectToLogin(w, r)
				return
			}
			id, err := uuid.Parse(chi.URLParam(r, "id"))
			if err != nil {
				s.renderError(w, r, http.StatusNotFound, "Database not found.")
				return
			}
			conn, err := s.store.GetDBConnection(r.Context(), id)
			if errors.Is(err, store.ErrNotFound) {
				s.renderError(w, r, http.StatusNotFound, "Database not found.")
				return
			}
			if err != nil {
				s.logger.ErrorContext(r.Context(), "load database", "error", err)
				s.renderError(w, r, http.StatusInternalServerError, "Could not load the database.")
				return
			}
			level, err := s.policy.Level(r.Context(), *user, id)
			if err != nil {
				s.logger.ErrorContext(r.Context(), "check access", "error", err)
				s.renderError(w, r, http.StatusInternalServerError, "Could not check access rights.")
				return
			}
			if !access.Can(level, action) {
				s.auditDenied(r, string(action), id.String())
				if user.IsPlatformAdmin {
					s.renderError(w, r, http.StatusForbidden, "Platform Admin accounts cannot access database tools.")
				} else {
					s.renderError(w, r, http.StatusForbidden, "You do not have sufficient access to this database.")
				}
				return
			}
			ctx := context.WithValue(r.Context(), dbKey, &conn)
			ctx = context.WithValue(ctx, levelKey, level)
			ctx = telemetry.WithDatabase(ctx, id.String(), conn.Name)
			if info := requestInfoFrom(ctx); info != nil {
				info.DBID = id.String()
				info.DBName = conn.Name
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (s *Server) auditDenied(r *http.Request, action, target string) {
	actorID := ""
	if user := currentUser(r); user != nil {
		actorID = user.ID.String()
	}
	s.logger.WarnContext(r.Context(), "access.denied",
		"actor_id", actorID,
		"target_id", target,
		"action", action,
		"status", "forbidden",
	)
}

func (s *Server) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	next := r.URL.RequestURI()
	if next == "/" || strings.HasPrefix(next, "/login") {
		next = ""
	}
	target := "/login"
	if next != "" {
		target += "?next=" + url.QueryEscape(next)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, raw string) {
	ttl := s.sessions.CookieTTL()
	http.SetCookie(w, &http.Cookie{
		Name:     "sid",
		Value:    raw,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
		Expires:  time.Now().Add(ttl),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "sid",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Unwrap preserves ResponseController/Flusher/Hijacker support.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page string, data pageData) {
	if data.User == nil {
		data.User = currentUser(r)
	}
	if data.CSRF == "" {
		if sess := currentSession(r); sess != nil {
			data.CSRF = sess.CSRFToken
		}
	}
	if data.Flash == nil {
		data.Flash = s.readFlash(w, r)
	}

	funcs := template.FuncMap{
		"levelLabel":      func(l domain.AccessLevel) string { return l.Label() },
		"statusLabel":     statusLabel,
		"relkindLabel":    relkindLabel,
		"constraintLabel": constraintLabel,
		"formatTime":      formatTime,
		"formatDuration":  formatDuration,
		"formatInt":       formatInt,
		"isoTime":         formatISOTime,
		"truncate":        truncate,
		"pageLinks":       pageLinks,
		"dict":            dict,
		"join":            strings.Join,
		"count":           countLabel,
		"rowsLabel":       rowsLabel,
		"lines":           func(s string) []string { return strings.Split(strings.TrimRight(s, "\n"), "\n") },
		"list":            func(v ...string) []string { return v },
		"add":             func(a, b int) int { return a + b },
		"sub":             func(a, b int) int { return a - b },
		"dash": func(v string) string {
			if strings.TrimSpace(v) == "" {
				return "—"
			}
			return v
		},
	}
	tmpl, err := template.New("layout").Funcs(funcs).ParseFS(assets, "templates/layout.html", "templates/partials/*.html", "templates/pages/"+page)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "parse template", "page", page, "error", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		s.logger.ErrorContext(r.Context(), "execute template", "page", page, "error", err)
	}
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, message string) {
	s.render(w, r, status, "system/error.html", pageData{Title: "Error", Message: message})
}

type pageData struct {
	Title      string
	Nav        string
	User       *domain.User
	CSRF       string
	Flash      *flashMessage
	Message    string
	Data       any
	DB         *domain.DBConnection
	Level      domain.AccessLevel
	TargetDown bool
	Preview    bool
	NoHeader   bool
}

type flashMessage struct {
	Kind    string
	Message string
}

func (s *Server) setFlash(w http.ResponseWriter, kind, message string) {
	value := kind + "|" + base64.RawURLEncoding.EncodeToString([]byte(message))
	http.SetCookie(w, &http.Cookie{
		Name:     "flash",
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60,
	})
}

func (s *Server) readFlash(w http.ResponseWriter, r *http.Request) *flashMessage {
	cookie, err := r.Cookie("flash")
	if err != nil || cookie.Value == "" {
		return nil
	}
	http.SetCookie(w, &http.Cookie{
		Name: "flash", Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
	parts := strings.SplitN(cookie.Value, "|", 2)
	if len(parts) != 2 {
		return nil
	}
	msg, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	return &flashMessage{Kind: parts[0], Message: string(msg)}
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
