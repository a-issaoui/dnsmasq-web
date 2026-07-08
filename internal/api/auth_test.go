package api

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testPassword = "correct horse battery staple"

func TestAuthDisabledEverythingIsOpen(t *testing.T) {
	ts := newTestServer(t, nil)
	c := noRedirects()

	if r := doJSON(t, c, "GET", ts.URL+"/", nil, nil); r.StatusCode != 200 {
		t.Fatalf("GET / = %d, want 200 with auth disabled", r.StatusCode)
	}
	if r := doJSON(t, c, "GET", ts.URL+"/api/conf", nil, nil); r.StatusCode != 200 {
		t.Fatalf("GET /api/conf = %d, want 200 with auth disabled", r.StatusCode)
	}
	// login page redirects home instead of dangling
	if r := doJSON(t, c, "GET", ts.URL+"/login", nil, nil); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET /login = %d, want 303 with auth disabled", r.StatusCode)
	}
	if r := doJSON(t, c, "POST", ts.URL+"/api/login", map[string]string{"password": "x"}, nil); r.StatusCode != 400 {
		t.Fatalf("POST /api/login = %d, want 400 with auth disabled", r.StatusCode)
	}
}

func TestAuthGatesPagesAndAPI(t *testing.T) {
	ts := newTestServer(t, func(c *Config) { c.AuthPassword = testPassword })
	c := noRedirects()

	// pages redirect, API answers JSON 401
	if r := doJSON(t, c, "GET", ts.URL+"/", nil, nil); r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/login" {
		t.Fatalf("GET / = %d loc=%q, want 303 → /login", r.StatusCode, r.Header.Get("Location"))
	}
	r := doJSON(t, c, "GET", ts.URL+"/api/conf", nil, nil)
	if r.StatusCode != 401 || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("GET /api/conf = %d %s, want JSON 401", r.StatusCode, r.Header.Get("Content-Type"))
	}
	// exempt surfaces stay reachable
	if r := doJSON(t, c, "GET", ts.URL+"/login", nil, nil); r.StatusCode != 200 {
		t.Fatalf("GET /login = %d, want 200", r.StatusCode)
	}
	if r := doJSON(t, c, "GET", ts.URL+"/static/style.css", nil, nil); r.StatusCode != 200 {
		t.Fatalf("GET /static/style.css = %d, want 200 (login page needs it)", r.StatusCode)
	}
}

func TestLoginLogoutFlow(t *testing.T) {
	ts := newTestServer(t, func(c *Config) { c.AuthPassword = testPassword })
	jar, _ := cookiejar.New(nil)
	c := noRedirects()
	c.Jar = jar

	// wrong password
	if r := doJSON(t, c, "POST", ts.URL+"/api/login", map[string]string{"password": "nope"}, nil); r.StatusCode != 401 {
		t.Fatalf("wrong password = %d, want 401", r.StatusCode)
	}

	// right password sets an HttpOnly strict cookie
	r := doJSON(t, c, "POST", ts.URL+"/api/login", map[string]string{"password": testPassword}, nil)
	if r.StatusCode != 200 {
		t.Fatalf("login = %d, want 200", r.StatusCode)
	}
	var sc *http.Cookie
	for _, ck := range r.Cookies() {
		if ck.Name == sessionCookie {
			sc = ck
		}
	}
	if sc == nil || !sc.HttpOnly || sc.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie must be HttpOnly + SameSite=Strict, got %+v", sc)
	}

	// session works for pages and API
	if r := doJSON(t, c, "GET", ts.URL+"/", nil, nil); r.StatusCode != 200 {
		t.Fatalf("GET / with session = %d, want 200", r.StatusCode)
	}
	if r := doJSON(t, c, "GET", ts.URL+"/api/conf", nil, nil); r.StatusCode != 200 {
		t.Fatalf("GET /api/conf with session = %d, want 200", r.StatusCode)
	}
	// logged-in visit to /login bounces home
	if r := doJSON(t, c, "GET", ts.URL+"/login", nil, nil); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET /login with session = %d, want 303", r.StatusCode)
	}

	// logout invalidates server-side state, not just the cookie
	if r := doJSON(t, c, "POST", ts.URL+"/api/logout", nil, nil); r.StatusCode != 200 {
		t.Fatalf("logout = %d", r.StatusCode)
	}
	u, _ := url.Parse(ts.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: sessionCookie, Value: sc.Value, Path: "/"}}) // replay old session id
	if r := doJSON(t, c, "GET", ts.URL+"/api/conf", nil, nil); r.StatusCode != 401 {
		t.Fatalf("replayed session after logout = %d, want 401", r.StatusCode)
	}
}

func TestLoginRateLimitLocksOut(t *testing.T) {
	ts := newTestServer(t, func(c *Config) { c.AuthPassword = testPassword })
	c := noRedirects()

	var last *http.Response
	for range loginMaxFails {
		last = doJSON(t, c, "POST", ts.URL+"/api/login", map[string]string{"password": "brute"}, nil)
	}
	if last.StatusCode != 401 {
		t.Fatalf("attempt %d = %d, want 401", loginMaxFails, last.StatusCode)
	}
	// next attempt — even with the CORRECT password — is locked out
	r := doJSON(t, c, "POST", ts.URL+"/api/login", map[string]string{"password": testPassword}, nil)
	if r.StatusCode != 429 || r.Header.Get("Retry-After") == "" {
		t.Fatalf("locked-out login = %d retry-after=%q, want 429 with Retry-After", r.StatusCode, r.Header.Get("Retry-After"))
	}
}

func TestBearerTokenForNonBrowserClients(t *testing.T) {
	ts := newTestServer(t, func(c *Config) {
		c.AuthPassword = testPassword
		c.APIToken = "shhh-mcp-token"
	})
	c := noRedirects()

	if r := doJSON(t, c, "GET", ts.URL+"/api/conf", nil, map[string]string{"Authorization": "Bearer shhh-mcp-token"}); r.StatusCode != 200 {
		t.Fatalf("valid bearer = %d, want 200", r.StatusCode)
	}
	if r := doJSON(t, c, "GET", ts.URL+"/api/conf", nil, map[string]string{"Authorization": "Bearer wrong"}); r.StatusCode != 401 {
		t.Fatalf("invalid bearer = %d, want 401", r.StatusCode)
	}
	// bearer disabled entirely when no API_TOKEN configured
	ts2 := newTestServer(t, func(c *Config) { c.AuthPassword = testPassword })
	if r := doJSON(t, c, "GET", ts2.URL+"/api/conf", nil, map[string]string{"Authorization": "Bearer shhh-mcp-token"}); r.StatusCode != 401 {
		t.Fatalf("bearer without API_TOKEN = %d, want 401", r.StatusCode)
	}
}

func TestUnauthenticatedMCPCallsNeverReachTheTracker(t *testing.T) {
	ts := newTestServer(t, func(c *Config) {
		c.AuthPassword = testPassword
		c.APIToken = "tok"
	})
	c := noRedirects()

	// unauthenticated MCP-tagged request is rejected at the door
	if r := doJSON(t, c, "GET", ts.URL+"/api/conf", nil, map[string]string{"X-MCP-Client": "rogue"}); r.StatusCode != 401 {
		t.Fatalf("unauthenticated MCP call = %d, want 401", r.StatusCode)
	}
	// and must not appear in the activity feed
	r := doJSON(t, c, "GET", ts.URL+"/api/mcp/status", nil, map[string]string{"Authorization": "Bearer tok"})
	var st mcpStatus
	if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if st.TotalCalls != 0 { // the status read itself is not MCP-tagged
		t.Fatalf("tracker recorded %d calls; unauthenticated traffic must not be tracked", st.TotalCalls)
	}
}

func TestSessionExpiryIsEnforced(t *testing.T) {
	a := newAuthManager("pw", "")
	sid, _, ok := a.login("pw", "127.0.0.1")
	if !ok {
		t.Fatal("login failed")
	}
	a.mu.Lock()
	a.sessions[sid] = time.Now().Add(-time.Minute) // force-expire
	a.mu.Unlock()

	req, _ := http.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid})
	if a.validSession(req) {
		t.Fatal("expired session must be invalid")
	}
}
