package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"dnsmasq-web/internal/dnsmasq"
)

// ─── Hub ──────────────────────────────────────────────────────────────────

type sseMsg struct {
	event string
	data  []byte
}

type sseClient struct {
	ch chan sseMsg
}

// Hub fans events out to connected SSE clients. Sends never block: a slow
// client just drops messages (each event type is self-contained snapshots or
// log lines, so drops are safe).
type Hub struct {
	mu      sync.Mutex
	clients map[*sseClient]struct{}
}

func NewHub() *Hub {
	return &Hub{clients: map[*sseClient]struct{}{}}
}

func (h *Hub) Subscribe() *sseClient {
	c := &sseClient{ch: make(chan sseMsg, 512)}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c
}

func (h *Hub) Unsubscribe(c *sseClient) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

func (h *Hub) Broadcast(event string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("sse marshal %s: %v", event, err)
		return
	}
	msg := sseMsg{event: event, data: data}
	h.mu.Lock()
	for c := range h.clients {
		select {
		case c.ch <- msg:
		default:
		}
	}
	h.mu.Unlock()
}

func (h *Hub) ClientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// ─── SSE endpoint ─────────────────────────────────────────────────────────

func (s *Server) apiEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 2000\n\n")
	fl.Flush()

	c := s.hub.Subscribe()
	defer s.hub.Unsubscribe(c)

	// Initial snapshot so a (re)connecting client is immediately consistent.
	s.sendSnapshot(c)

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case m := <-c.ch:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", m.event, m.data)
			fl.Flush()
		}
	}
}

func (s *Server) sendSnapshot(c *sseClient) {
	push := func(event string, v any) {
		data, err := json.Marshal(v)
		if err != nil {
			return
		}
		select {
		case c.ch <- sseMsg{event: event, data: data}:
		default:
		}
	}
	push("status", s.currentStatus())
	push("mcp", s.mcp.snapshot())
	if leases, err := dnsmasq.ParseLeases(s.cfg.DnsmasqLeases); err == nil {
		push("leases", leases)
	}
	if conf, err := dnsmasq.LoadConf(s.cfg.DnsmasqConf); err == nil {
		push("config", map[string]string{"rev": conf.Rev})
	}
}

// ─── Producers ────────────────────────────────────────────────────────────

// startProducers launches the background watchers that feed the hub.
func (s *Server) startProducers(ctx context.Context) {
	go s.watchLoop(ctx)
	go s.journalLoop(ctx)
}

// watchLoop polls cheap local state: service status (3s), lease-file and
// config-file mtimes (2s). Events are only emitted on change.
func (s *Server) watchLoop(ctx context.Context) {
	var lastLeaseMod, lastConfMod time.Time
	var lastStatus *statusPayload
	statusTick := 0

	if fi, err := os.Stat(s.cfg.DnsmasqLeases); err == nil {
		lastLeaseMod = fi.ModTime()
	}
	if fi, err := os.Stat(s.cfg.DnsmasqConf); err == nil {
		lastConfMod = fi.ModTime()
	}

	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if s.hub.ClientCount() == 0 {
			continue
		}

		if fi, err := os.Stat(s.cfg.DnsmasqLeases); err == nil && !fi.ModTime().Equal(lastLeaseMod) {
			lastLeaseMod = fi.ModTime()
			if leases, err := dnsmasq.ParseLeases(s.cfg.DnsmasqLeases); err == nil {
				s.hub.Broadcast("leases", leases)
			}
		}
		if fi, err := os.Stat(s.cfg.DnsmasqConf); err == nil && !fi.ModTime().Equal(lastConfMod) {
			lastConfMod = fi.ModTime()
			if conf, err := dnsmasq.LoadConf(s.cfg.DnsmasqConf); err == nil {
				s.hub.Broadcast("config", map[string]string{"rev": conf.Rev})
			}
		}

		statusTick++
		if statusTick%2 != 0 { // status every ~4s, it shells out to systemctl
			continue
		}
		st := s.currentStatus()
		if lastStatus == nil || !reflect.DeepEqual(*st, *lastStatus) {
			lastStatus = st
			s.hub.Broadcast("status", st)
		}
	}
}

// journalLoop keeps one `journalctl -f` subprocess alive and turns its lines
// into "log" events plus parsed "query" / "dhcp" activity events.
func (s *Server) journalLoop(ctx context.Context) {
	for {
		ch, err := s.svc.FollowJournal(ctx)
		if err != nil {
			log.Printf("journal follow unavailable: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(15 * time.Second):
				continue
			}
		}
		for line := range ch {
			s.hub.Broadcast("log", map[string]string{"line": line})
			if ev := parseActivity(line); ev != nil {
				s.hub.Broadcast(ev.Stream, ev)
			}
		}
		select { // journalctl died; back off and restart it
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

// ─── Activity parsing ─────────────────────────────────────────────────────

// ActivityEvent is a structured view of one interesting dnsmasq log line.
type ActivityEvent struct {
	Stream string `json:"-"`               // "query" or "dhcp"
	TS     string `json:"ts"`              // HH:MM:SS
	Kind   string `json:"kind"`            // query|forwarded|reply|cached|config|hosts / discover|offer|request|ack|nak|release
	RType  string `json:"rtype,omitempty"` // A, AAAA, PTR… for kind=query
	Name   string `json:"name,omitempty"`
	Value  string `json:"value,omitempty"` // answer / server / ip
	Client string `json:"client,omitempty"`
	MAC    string `json:"mac,omitempty"`
	Iface  string `json:"iface,omitempty"`
}

var (
	reJournal = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T(\d{2}:\d{2}:\d{2})\S*)\s+\S+\s+dnsmasq(?:-dhcp)?\[\d+\]:\s*(.*)$`)
	reQuery   = regexp.MustCompile(`^query\[([A-Z0-9]+)\]\s+(\S+)\s+from\s+(\S+)`)
	reFwd     = regexp.MustCompile(`^forwarded\s+(\S+)\s+to\s+(\S+)`)
	reReply   = regexp.MustCompile(`^(reply|cached|config|/etc/hosts|DHCP)\s+(\S+)\s+is\s+(.+)$`)
	reDHCP    = regexp.MustCompile(`^(DHCPDISCOVER|DHCPOFFER|DHCPREQUEST|DHCPACK|DHCPNAK|DHCPRELEASE|DHCPINFORM|SOLICIT|ADVERTISE|REQUEST6|REPLY6)\(([^)]+)\)\s*(\S*)\s*(\S*)\s*(.*)$`)
)

func parseActivity(line string) *ActivityEvent {
	m := reJournal.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	ts, msg := m[2], m[3]

	if q := reQuery.FindStringSubmatch(msg); q != nil {
		return &ActivityEvent{Stream: "query", TS: ts, Kind: "query", RType: q[1], Name: q[2], Client: q[3]}
	}
	if f := reFwd.FindStringSubmatch(msg); f != nil {
		return &ActivityEvent{Stream: "query", TS: ts, Kind: "forwarded", Name: f[1], Value: f[2]}
	}
	if r := reReply.FindStringSubmatch(msg); r != nil {
		kind := r[1]
		switch kind {
		case "/etc/hosts":
			kind = "hosts"
		case "DHCP":
			kind = "dhcp-lease"
		}
		return &ActivityEvent{Stream: "query", TS: ts, Kind: strings.ToLower(kind), Name: r[2], Value: r[3]}
	}
	if d := reDHCP.FindStringSubmatch(msg); d != nil {
		ev := &ActivityEvent{Stream: "dhcp", TS: ts, Kind: strings.ToLower(strings.TrimPrefix(d[1], "DHCP")), Iface: d[2]}
		ev.Value = d[3] // usually the IP
		ev.MAC = d[4]
		ev.Name = strings.TrimSpace(d[5])
		return ev
	}
	return nil
}
