package api

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// MCP integration: an AI agent drives dnsmasq through this console's JSON API
// via the dnsmasq-web MCP server, which tags every request with an
// `X-MCP-Client` header. That lets us (1) show live MCP activity on the MCP
// page and (2) gate MCP-originated writes behind a read-only kill-switch the
// operator flips from the UI. Reads are never gated.

const mcpRecentCap = 25

// mcpConnectedWindow — how recently the MCP must have called for the UI to
// show it as "connected". The MCP is a stdio process spawned on demand, so
// "connected" really means "active very recently".
const mcpConnectedWindow = 90 * time.Second

type mcpCall struct {
	Method  string    `json:"method"`
	Path    string    `json:"path"`
	At      time.Time `json:"at"`
	Blocked bool      `json:"blocked"`
}

type mcpTracker struct {
	mu       sync.Mutex
	client   string
	lastSeen time.Time
	total    int64
	blocked  int64
	writesOn bool
	recent   []mcpCall // newest first, capped at mcpRecentCap
}

func newMCPTracker() *mcpTracker { return &mcpTracker{writesOn: true} }

func (m *mcpTracker) record(client, method, path string, blocked bool) {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.client = client
	m.lastSeen = now
	m.total++
	if blocked {
		m.blocked++
	}
	m.recent = append([]mcpCall{{Method: method, Path: path, At: now, Blocked: blocked}}, m.recent...)
	if len(m.recent) > mcpRecentCap {
		m.recent = m.recent[:mcpRecentCap]
	}
}

func (m *mcpTracker) writesAllowed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writesOn
}

func (m *mcpTracker) setWrites(on bool) {
	m.mu.Lock()
	m.writesOn = on
	m.mu.Unlock()
}

type mcpStatus struct {
	Client        string     `json:"client"`
	SeenEver      bool       `json:"seen_ever"`
	Connected     bool       `json:"connected"`
	LastSeen      *time.Time `json:"last_seen"`
	TotalCalls    int64      `json:"total_calls"`
	BlockedCalls  int64      `json:"blocked_calls"`
	WritesAllowed bool       `json:"writes_allowed"`
	Recent        []mcpCall  `json:"recent"`
}

func (m *mcpTracker) snapshot() mcpStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := mcpStatus{
		Client:        m.client,
		TotalCalls:    m.total,
		BlockedCalls:  m.blocked,
		WritesAllowed: m.writesOn,
		Recent:        append([]mcpCall{}, m.recent...),
	}
	if !m.lastSeen.IsZero() {
		ls := m.lastSeen
		st.SeenEver = true
		st.LastSeen = &ls
		st.Connected = time.Since(m.lastSeen) < mcpConnectedWindow
	}
	return st
}

// isMCPWrite reports whether an MCP-originated request would mutate state.
// Mutating = POST/PUT/DELETE, except the non-mutating validate endpoint and
// the MCP control plane itself (which is browser-origin anyway).
func isMCPWrite(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodDelete:
	default:
		return false
	}
	p := r.URL.Path
	if p == "/api/conf/validate" || strings.HasPrefix(p, "/api/mcp/") {
		return false
	}
	return strings.HasPrefix(p, "/api/")
}

// ─── HTTP handlers ──────────────────────────────────────────────────────────

func (s *Server) apiMCPStatus(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, s.mcp.snapshot())
}

func (s *Server) apiMCPSetWrites(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Allowed bool `json:"allowed"`
	}
	if err := decode(r, &p); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	s.mcp.setWrites(p.Allowed)
	s.hub.Broadcast("mcp", s.mcp.snapshot())
	msg := "MCP writes enabled — the agent can change dnsmasq again"
	if !p.Allowed {
		msg = "MCP set to read-only — the agent can read but not change anything"
	}
	jsonOK(w, map[string]any{"message": msg, "writes_allowed": p.Allowed})
}
