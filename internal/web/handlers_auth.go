package web

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"leafygo/internal/auth"
	"leafygo/internal/domain"
	"leafygo/internal/store"
)

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("sid"); err == nil && cookie.Value != "" {
		if _, newRaw, err := s.sessions.Resolve(r.Context(), cookie.Value); err == nil {
			// Resolve may rotate the identifier; always send the replacement
			// back before redirecting, otherwise the old cookie dies after grace.
			if newRaw != "" {
				s.setSessionCookie(w, newRaw)
			}
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		s.clearSessionCookie(w)
	}
	token, err := s.loginCSRFToken(w, r)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "Gagal menyiapkan form login.")
		return
	}
	s.render(w, r, http.StatusOK, "login.html", pageData{
		Title: "Masuk",
		CSRF:  token,
		Data:  map[string]string{"Next": safeNext(r.URL.Query().Get("next"))},
	})
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, http.StatusBadRequest, "Form login tidak valid.")
		return
	}
	cookie, err := r.Cookie("csrf_login")
	if err != nil || !constantTimeEqual(r.FormValue("csrf_token"), cookie.Value) {
		s.renderError(w, r, http.StatusForbidden, "Token CSRF login tidak valid. Muat ulang halaman.")
		return
	}

	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	key := clientIP(r) + "|" + strings.ToLower(username)
	if !s.limiter.allowed(key) {
		s.setFlash(w, "error", "Terlalu banyak percobaan login. Coba lagi beberapa menit lagi.")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	user, err := s.store.GetUserByUsername(r.Context(), username)
	if errors.Is(err, store.ErrNotFound) {
		auth.PerformDummyVerify(password)
		s.limiter.fail(key)
		s.setFlash(w, "error", "Username atau password salah.")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.logger.Error("login lookup", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "Gagal memproses login.")
		return
	}
	ok, err := auth.VerifyPassword(user.PasswordHash, password)
	if err != nil || !ok {
		s.limiter.fail(key)
		s.setFlash(w, "error", "Username atau password salah.")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.limiter.reset(key)

	// A fresh identifier is issued on every login. Any previous browser
	// session is revoked so a fixated identifier cannot survive authentication.
	if old, err := r.Cookie("sid"); err == nil && old.Value != "" {
		_ = s.sessions.Revoke(r.Context(), old.Value)
	}
	raw, _, err := s.sessions.Create(r.Context(), user.ID)
	if err != nil {
		s.logger.Error("create session", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "Gagal membuat sesi.")
		return
	}
	s.setSessionCookie(w, raw)
	http.SetCookie(w, &http.Cookie{Name: "csrf_login", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.cfg.CookieSecure})
	s.setFlash(w, "success", "Selamat datang, "+user.Username+".")
	http.Redirect(w, r, safeNext(r.FormValue("next")), http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("sid"); err == nil && cookie.Value != "" {
		if err := s.sessions.Revoke(r.Context(), cookie.Value); err != nil {
			s.logger.Error("revoke session", "error", err)
		}
	}
	s.clearSessionCookie(w)
	s.setFlash(w, "success", "Anda sudah keluar.")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	dbs, err := s.store.ListAccessibleDBs(r.Context(), user.ID)
	if err != nil {
		s.logger.Error("list accessible databases", "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "Gagal memuat daftar database.")
		return
	}
	s.render(w, r, http.StatusOK, "dashboard.html", pageData{
		Title: "Database",
		Nav:   "databases",
		Data:  dashboardData{Databases: dbs, IsAdmin: user.IsPlatformAdmin},
	})
}

type dashboardData struct {
	Databases []domain.AccessibleDB
	IsAdmin   bool
}

func (s *Server) loginCSRFToken(w http.ResponseWriter, r *http.Request) (string, error) {
	if cookie, err := r.Cookie("csrf_login"); err == nil && len(cookie.Value) >= 32 {
		return cookie.Value, nil
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "csrf_login",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	return token, nil
}

func safeNext(next string) string {
	next = strings.TrimSpace(next)
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	if strings.ContainsAny(next, "\\\r\n") || strings.HasPrefix(next, "/login") {
		return "/"
	}
	return next
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type loginLimiter struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	failures map[string][]time.Time
	now      func() time.Time
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{max: max, window: window, failures: make(map[string][]time.Time), now: time.Now}
}

func (l *loginLimiter) allowed(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := l.now().Add(-l.window)
	kept := l.failures[key][:0]
	for _, t := range l.failures[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	l.failures[key] = kept
	return len(kept) < l.max
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[key] = append(l.failures[key], l.now())
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}
