package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Authentication is opt-in: setting AUTH_PASSWORD enables it. Browsers log in
// once and hold an HttpOnly SameSite=Strict session cookie; non-browser
// clients (the MCP server, curl scripting) authenticate per-request with
// `Authorization: Bearer $API_TOKEN`. Both secrets live in the systemd unit
// (root-only readable) and are compared in constant time against SHA-256
// digests, so neither plaintext is kept in process memory.

const (
	sessionCookie = "dnsmasq_web_session"
	sessionTTL    = 30 * 24 * time.Hour
	loginMaxFails = 5
	loginLockout  = 60 * time.Second
	maxSessions   = 100 // oldest evicted beyond this; a home box never gets close
	tokenBytes    = 32
)

type authManager struct {
	enabled      bool
	passwordHash [sha256.Size]byte
	tokenSet     bool
	tokenHash    [sha256.Size]byte

	mu       sync.Mutex
	sessions map[string]time.Time // session id → expiry
	fails    map[string]failState // login throttle, keyed by client IP
}

type failState struct {
	count       int
	lockedUntil time.Time
}

func newAuthManager(password, token string) *authManager {
	a := &authManager{
		sessions: map[string]time.Time{},
		fails:    map[string]failState{},
	}
	if password != "" {
		a.enabled = true
		a.passwordHash = sha256.Sum256([]byte(password))
	}
	if token != "" {
		a.tokenSet = true
		a.tokenHash = sha256.Sum256([]byte(token))
	}
	return a
}

// login validates the password with per-IP throttling. On success it returns
// a fresh session id; on lockout it returns the remaining wait.
func (a *authManager) login(password, ip string) (sid string, wait time.Duration, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	st := a.fails[ip]
	if now.Before(st.lockedUntil) {
		return "", time.Until(st.lockedUntil), false
	}

	h := sha256.Sum256([]byte(password))
	if subtle.ConstantTimeCompare(h[:], a.passwordHash[:]) != 1 {
		st.count++
		if st.count >= loginMaxFails {
			st.lockedUntil = now.Add(loginLockout)
			st.count = 0
		}
		a.fails[ip] = st
		return "", 0, false
	}

	delete(a.fails, ip)
	a.pruneLocked(now)
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", 0, false
	}
	sid = hex.EncodeToString(buf)
	a.sessions[sid] = now.Add(sessionTTL)
	return sid, 0, true
}

func (a *authManager) validSession(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.sessions[c.Value]
	if !ok {
		return false
	}
	now := time.Now()
	if now.After(exp) {
		delete(a.sessions, c.Value)
		return false
	}
	a.sessions[c.Value] = now.Add(sessionTTL) // sliding expiry
	return true
}

func (a *authManager) validBearer(r *http.Request) bool {
	if !a.tokenSet {
		return false
	}
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || tok == "" {
		return false
	}
	h := sha256.Sum256([]byte(tok))
	return subtle.ConstantTimeCompare(h[:], a.tokenHash[:]) == 1
}

func (a *authManager) logout(r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.mu.Unlock()
	}
}

// pruneLocked drops expired sessions and, if still over capacity, the ones
// expiring soonest. Callers must hold a.mu.
func (a *authManager) pruneLocked(now time.Time) {
	for sid, exp := range a.sessions {
		if now.After(exp) {
			delete(a.sessions, sid)
		}
	}
	for len(a.sessions) >= maxSessions {
		oldest, oldestExp := "", now.Add(sessionTTL*2)
		for sid, exp := range a.sessions {
			if exp.Before(oldestExp) {
				oldest, oldestExp = sid, exp
			}
		}
		delete(a.sessions, oldest)
	}
}

// authExempt lists what must stay reachable without a session: the login
// page and its endpoint, and the assets the login page itself needs.
func authExempt(r *http.Request) bool {
	p := r.URL.Path
	return p == "/login" || p == "/api/login" || strings.HasPrefix(p, "/static/")
}

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// ─── HTTP handlers ──────────────────────────────────────────────────────────

func (s *Server) apiLogin(w http.ResponseWriter, r *http.Request) {
	if !s.auth.enabled {
		jsonErr(w, 400, "authentication is not enabled")
		return
	}
	var p struct {
		Password string `json:"password"`
	}
	if err := decode(r, &p); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	sid, wait, ok := s.auth.login(p.Password, clientIP(r))
	if !ok {
		if wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			jsonErr(w, 429, "too many attempts — try again in "+wait.Round(time.Second).String())
			return
		}
		jsonErr(w, 401, "wrong password")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sid,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	jsonOK(w, map[string]string{"message": "Welcome back"})
}

func (s *Server) apiLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.logout(r)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	jsonOK(w, map[string]string{"message": "Logged out"})
}

func (s *Server) pageLogin(w http.ResponseWriter, r *http.Request) {
	if !s.auth.enabled || s.auth.validSession(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	t, err := s.loadTemplate("login.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = t.ExecuteTemplate(w, "login.html", nil)
}

// requireAuth is the middleware gate. Pages bounce to /login; API calls get
// a JSON 401 the frontend turns into a redirect.
func (s *Server) requireAuth(w http.ResponseWriter, r *http.Request) (allowed bool) {
	if !s.auth.enabled || authExempt(r) {
		return true
	}
	if s.auth.validSession(r) || s.auth.validBearer(r) {
		return true
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		jsonErr(w, 401, "authentication required")
		return false
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
	return false
}
