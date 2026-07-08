package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestIsMCPWriteAndControlPlane(t *testing.T) {
	tests := []struct {
		method  string
		path    string
		write   bool
		control bool
	}{
		{"GET", "/api/conf", false, false},
		{"GET", "/api/mcp/status", false, false},
		{"POST", "/api/conf/lines", true, false},
		{"PUT", "/api/conf/scalar", true, false},
		{"DELETE", "/api/backups/x.bak", true, false},
		{"POST", "/api/conf/validate", false, false}, // non-mutating by design
		{"POST", "/api/service/restart", true, false},
		{"PUT", "/api/mcp/writes", true, true}, // the kill-switch itself
		{"POST", "/api/mcp/anything", true, true},
		{"GET", "/", false, false},
		{"POST", "/login", false, false}, // not under /api/
	}
	for _, tt := range tests {
		r, _ := http.NewRequest(tt.method, tt.path, nil)
		if got := isMCPWrite(r); got != tt.write {
			t.Errorf("isMCPWrite(%s %s) = %v, want %v", tt.method, tt.path, got, tt.write)
		}
		if got := isMCPControlPlane(r); got != tt.control {
			t.Errorf("isMCPControlPlane(%s %s) = %v, want %v", tt.method, tt.path, got, tt.control)
		}
	}
}

func TestTrackerRecordSnapshotAndCap(t *testing.T) {
	tr := newMCPTracker()

	if st := tr.snapshot(); st.SeenEver || st.Connected || !st.WritesAllowed {
		t.Fatalf("fresh tracker state wrong: %+v", st)
	}

	for i := range mcpRecentCap + 10 {
		tr.record("claude", "GET", fmt.Sprintf("/api/conf?%d", i), false)
	}
	tr.record("claude", "POST", "/api/conf/lines", true)

	st := tr.snapshot()
	if st.TotalCalls != int64(mcpRecentCap+11) || st.BlockedCalls != 1 {
		t.Fatalf("counts wrong: total=%d blocked=%d", st.TotalCalls, st.BlockedCalls)
	}
	if len(st.Recent) != mcpRecentCap {
		t.Fatalf("recent list must cap at %d, got %d", mcpRecentCap, len(st.Recent))
	}
	if !st.Recent[0].Blocked || st.Recent[0].Path != "/api/conf/lines" {
		t.Fatalf("newest call must be first: %+v", st.Recent[0])
	}
	if !st.Connected || !st.SeenEver || st.Client != "claude" {
		t.Fatalf("freshly active tracker must show connected: %+v", st)
	}

	// snapshot must be a copy — mutating it can't corrupt the tracker
	st.Recent[0].Path = "tampered"
	if tr.snapshot().Recent[0].Path == "tampered" {
		t.Fatal("snapshot leaks internal slice")
	}
}

func TestTrackerConnectedWindow(t *testing.T) {
	tr := newMCPTracker()
	tr.record("claude", "GET", "/api/conf", false)
	tr.mu.Lock()
	tr.lastSeen = time.Now().Add(-mcpConnectedWindow - time.Second)
	tr.mu.Unlock()
	if st := tr.snapshot(); st.Connected || !st.SeenEver {
		t.Fatalf("stale activity must be SeenEver but not Connected: %+v", st)
	}
}

func TestMCPKillSwitchEndToEnd(t *testing.T) {
	ts := newTestServer(t, nil) // auth disabled: gate must work on its own
	c := noRedirects()
	agent := map[string]string{"X-MCP-Client": "claude-code"}

	// agent can never toggle the kill-switch, even in writable mode
	if r := doJSON(t, c, "PUT", ts.URL+"/api/mcp/writes", map[string]bool{"allowed": false}, agent); r.StatusCode != 403 {
		t.Fatalf("agent toggling kill-switch = %d, want 403", r.StatusCode)
	}

	// operator (no MCP header) flips to read-only
	if r := doJSON(t, c, "PUT", ts.URL+"/api/mcp/writes", map[string]bool{"allowed": false}, nil); r.StatusCode != 200 {
		t.Fatalf("operator toggle = %d, want 200", r.StatusCode)
	}

	// read-only: agent reads pass, agent writes blocked
	if r := doJSON(t, c, "GET", ts.URL+"/api/conf", nil, agent); r.StatusCode != 200 {
		t.Fatalf("agent read in read-only mode = %d, want 200", r.StatusCode)
	}
	if r := doJSON(t, c, "POST", ts.URL+"/api/conf/lines",
		map[string]string{"key": "server", "value": "9.9.9.9"}, agent); r.StatusCode != 403 {
		t.Fatalf("agent write in read-only mode = %d, want 403", r.StatusCode)
	}
	// agent cannot re-enable itself
	if r := doJSON(t, c, "PUT", ts.URL+"/api/mcp/writes", map[string]bool{"allowed": true}, agent); r.StatusCode != 403 {
		t.Fatalf("agent re-enabling writes = %d, want 403", r.StatusCode)
	}

	// UI traffic is never gated by the MCP switch
	if r := doJSON(t, c, "GET", ts.URL+"/api/service/status", nil, nil); r.StatusCode != 200 {
		t.Fatalf("browser request gated = %d, want 200", r.StatusCode)
	}

	// the whole story is visible in the feed: blocked calls flagged
	r := doJSON(t, c, "GET", ts.URL+"/api/mcp/status", nil, nil)
	var st mcpStatus
	if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if st.BlockedCalls != 3 { // toggle attempt + write + re-enable attempt
		t.Fatalf("want 3 blocked calls in the feed, got %d", st.BlockedCalls)
	}
	if st.WritesAllowed {
		t.Fatal("writes must still be disabled")
	}

	// operator re-enables; agent write now passes through to the handler
	if r := doJSON(t, c, "PUT", ts.URL+"/api/mcp/writes", map[string]bool{"allowed": true}, nil); r.StatusCode != 200 {
		t.Fatalf("operator re-enable = %d, want 200", r.StatusCode)
	}
	if r := doJSON(t, c, "POST", ts.URL+"/api/conf/lines",
		map[string]string{"key": "server", "value": "9.9.9.9"}, agent); r.StatusCode != 200 {
		t.Fatalf("agent write in writable mode = %d, want 200", r.StatusCode)
	}
}
