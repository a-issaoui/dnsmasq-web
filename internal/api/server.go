package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"dnsmasq-web/internal/dnsmasq"
	"dnsmasq-web/internal/service"
)

type Config struct {
	DnsmasqConf   string
	DnsmasqLeases string
	BackupDir     string
	Host          string
	Port          string
	TemplateDir   string
	StaticDir     string
}

type Server struct {
	cfg    *Config
	writer *dnsmasq.Writer
	svc    *service.Manager
	hub    *Hub
	mcp    *mcpTracker

	confMu  sync.Mutex // serialises all config mutations
	funcMap template.FuncMap
}

func NewServer(cfg *Config) (*Server, error) {
	s := &Server{
		cfg:    cfg,
		writer: dnsmasq.NewWriter(cfg.DnsmasqConf, cfg.BackupDir),
		svc:    service.New("dnsmasq"),
		hub:    NewHub(),
		mcp:    newMCPTracker(),
	}
	s.funcMap = template.FuncMap{
		"formatTime":  func(t time.Time) string { return t.Format("Jan 02 15:04") },
		"formatBytes": formatBytes,
	}
	if _, err := s.loadPage("index.html"); err != nil {
		return nil, fmt.Errorf("template error: %w", err)
	}
	return s, nil
}

// Start launches background event producers.
func (s *Server) Start(ctx context.Context) { s.startProducers(ctx) }

func formatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KB", float64(n)/1024)
}

// ─── Routing ──────────────────────────────────────────────────────────────

func (s *Server) SetupRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(s.cfg.StaticDir))))

	pages := map[string]string{
		"/":         "index.html",
		"/dns":      "dns.html",
		"/dhcp":     "dhcp.html",
		"/tftp":     "tftp.html",
		"/network":  "network.html",
		"/settings": "settings.html",
		"/config":   "config.html",
		"/logs":     "logs.html",
		"/backups":  "backups.html",
		"/mcp":      "mcp.html",
	}
	titles := map[string]string{
		"/": "Dashboard", "/dns": "DNS", "/dhcp": "DHCP", "/tftp": "TFTP",
		"/network": "Network", "/settings": "Settings", "/config": "Config File",
		"/logs": "Logs", "/backups": "Backups", "/mcp": "MCP",
	}
	for path, tmpl := range pages {
		p, t := path, tmpl
		pattern := "GET " + p
		id := strings.TrimPrefix(p, "/")
		if p == "/" {
			pattern, id = "GET /{$}", "index"
		}
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			s.renderPage(w, t, titles[p], id)
		})
	}

	// Config model
	mux.HandleFunc("GET /api/schema", s.apiSchema)
	mux.HandleFunc("GET /api/conf", s.apiGetConf)
	mux.HandleFunc("GET /api/conf/raw", s.apiGetRaw)
	mux.HandleFunc("PUT /api/conf/raw", s.apiPutRaw)
	mux.HandleFunc("POST /api/conf/validate", s.apiValidate)
	mux.HandleFunc("POST /api/conf/lines", s.apiAddLine)
	mux.HandleFunc("PUT /api/conf/lines/{idx}", s.apiUpdateLine)
	mux.HandleFunc("DELETE /api/conf/lines/{idx}", s.apiDeleteLine)
	mux.HandleFunc("PUT /api/conf/scalar", s.apiSetScalar)
	mux.HandleFunc("PUT /api/conf/flag", s.apiSetFlag)

	// Service
	mux.HandleFunc("GET /api/service/status", s.apiServiceStatus)
	mux.HandleFunc("POST /api/service/{action}", s.apiServiceAction)
	mux.HandleFunc("GET /api/service/logs", s.apiServiceLogs)

	// Encrypted upstream (dnscrypt-proxy)
	mux.HandleFunc("GET /api/encdns", s.apiEncDNSStatus)
	mux.HandleFunc("PUT /api/encdns", s.apiEncDNSSet)

	// Resolver health / browser-bypass detection
	mux.HandleFunc("GET /api/resolver-check", s.apiResolverCheck)
	mux.HandleFunc("GET /api/resolver-check/verify", s.apiResolverVerify)

	// Data
	mux.HandleFunc("GET /api/dhcp/leases", s.apiGetLeases)
	mux.HandleFunc("GET /api/interfaces", s.apiInterfaces)
	mux.HandleFunc("GET /api/lookup", s.apiLookup)

	// Backups
	mux.HandleFunc("GET /api/backups", s.apiGetBackups)
	mux.HandleFunc("POST /api/backups", s.apiCreateBackup)
	mux.HandleFunc("GET /api/backups/{name}", s.apiReadBackup)
	mux.HandleFunc("POST /api/backups/restore", s.apiRestoreBackup)
	mux.HandleFunc("DELETE /api/backups/{name}", s.apiDeleteBackup)

	// MCP integration (activity + read-only kill-switch)
	mux.HandleFunc("GET /api/mcp/status", s.apiMCPStatus)
	mux.HandleFunc("PUT /api/mcp/writes", s.apiMCPSetWrites)

	// Realtime
	mux.HandleFunc("GET /api/events", s.apiEvents)

	return s.withMiddleware(mux)
}

func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// MCP-originated requests carry X-MCP-Client. Track them for the MCP
		// page, and enforce the read-only kill-switch on writes. The switch
		// itself is operator-only: an agent can never toggle it.
		if client := r.Header.Get("X-MCP-Client"); client != "" {
			control := isMCPControlPlane(r)
			blocked := control || (isMCPWrite(r) && !s.mcp.writesAllowed())
			s.mcp.record(client, r.Method, r.URL.Path, blocked)
			if s.hub.ClientCount() > 0 {
				s.hub.Broadcast("mcp", s.mcp.snapshot())
			}
			if blocked {
				msg := "MCP writes are disabled from the dnsmasq-web console (read-only mode). Re-enable on the MCP page to allow changes."
				if control {
					msg = "The MCP write kill-switch is operator-only — change it from the dnsmasq-web console, not through the agent."
				}
				log.Printf("MCP %s %s — BLOCKED", r.Method, r.URL.Path)
				jsonErr(w, 403, msg)
				return
			}
		}

		start := time.Now()
		next.ServeHTTP(w, r)
		if r.URL.Path != "/api/events" {
			log.Printf("%s %s — %s", r.Method, r.URL.Path, time.Since(start).Round(time.Microsecond))
		}
	})
}

// ─── Templates ────────────────────────────────────────────────────────────

type PageData struct {
	Title    string
	Page     string
	Version  string
	ConfPath string
	Status   *dnsmasq.ServiceStatus
}

func (s *Server) loadPage(page string) (*template.Template, error) {
	t := template.New("").Funcs(s.funcMap)
	for _, name := range []string{"base.html", page} {
		data, err := os.ReadFile(filepath.Join(s.cfg.TemplateDir, name))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if _, err := t.New(name).Parse(string(data)); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
	}
	return t, nil
}

func (s *Server) renderPage(w http.ResponseWriter, page, title, id string) {
	t, err := s.loadPage(page)
	if err != nil {
		log.Printf("load page %s: %v", page, err)
		http.Error(w, "Template error: "+err.Error(), 500)
		return
	}
	status, _ := s.svc.Status()
	data := PageData{
		Title: title, Page: id,
		Version:  s.svc.Version(),
		ConfPath: s.cfg.DnsmasqConf,
		Status:   status,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "base.html", data); err != nil {
		log.Printf("execute page %s: %v", page, err)
	}
}

// ─── JSON helpers ─────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// ─── Config API ───────────────────────────────────────────────────────────

func (s *Server) apiSchema(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "max-age=60")
	jsonOK(w, map[string]any{
		"categories": dnsmasq.Categories,
		"directives": dnsmasq.Registry,
	})
}

func (s *Server) apiGetConf(w http.ResponseWriter, r *http.Request) {
	conf, err := dnsmasq.LoadConf(s.cfg.DnsmasqConf)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	jsonOK(w, conf)
}

func (s *Server) apiGetRaw(w http.ResponseWriter, r *http.Request) {
	conf, err := dnsmasq.LoadConf(s.cfg.DnsmasqConf)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	jsonOK(w, map[string]string{"content": conf.Serialize(), "rev": conf.Rev, "path": conf.Path})
}

// mutateConf loads a fresh copy of the config, applies fn, validates and
// atomically writes the result, then answers with the new parsed config.
func (s *Server) mutateConf(w http.ResponseWriter, fn func(c *dnsmasq.ConfFile) error) {
	s.confMu.Lock()
	defer s.confMu.Unlock()

	conf, err := dnsmasq.LoadConf(s.cfg.DnsmasqConf)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	if err := fn(conf); err != nil {
		var conflict *dnsmasq.ConflictError
		if errors.As(err, &conflict) {
			jsonErr(w, 409, err.Error())
			return
		}
		jsonErr(w, 400, err.Error())
		return
	}
	if err := s.writer.WriteRaw(conf.Serialize()); err != nil {
		if strings.Contains(err.Error(), "rejected by dnsmasq --test") {
			jsonErr(w, 422, err.Error())
			return
		}
		jsonErr(w, 500, err.Error())
		return
	}
	fresh, err := dnsmasq.LoadConf(s.cfg.DnsmasqConf)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	s.hub.Broadcast("config", map[string]string{"rev": fresh.Rev})
	jsonOK(w, fresh)
}

type linePayload struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Flag      bool   `json:"flag"`
	Raw       string `json:"raw"`        // alternative to key/value for arbitrary edits
	ExpectRaw string `json:"expect_raw"` // optimistic concurrency guard
}

func validKey(key string) error {
	if key == "" {
		return fmt.Errorf("directive key is required")
	}
	for _, r := range key {
		if !(r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return fmt.Errorf("invalid directive name %q", key)
		}
	}
	return nil
}

func validValue(v string) error {
	if strings.ContainsAny(v, "\n\r") {
		return fmt.Errorf("value must not contain newlines")
	}
	return nil
}

func (s *Server) apiAddLine(w http.ResponseWriter, r *http.Request) {
	var p linePayload
	if err := decode(r, &p); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if err := validKey(p.Key); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if err := validValue(p.Value); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if !p.Flag && strings.TrimSpace(p.Value) == "" {
		jsonErr(w, 400, "value is required")
		return
	}
	s.mutateConf(w, func(c *dnsmasq.ConfFile) error {
		c.AddLine(p.Key, p.Value, p.Flag)
		return nil
	})
}

func (s *Server) apiUpdateLine(w http.ResponseWriter, r *http.Request) {
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil {
		jsonErr(w, 400, "invalid line index")
		return
	}
	var p linePayload
	if err := decode(r, &p); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	s.mutateConf(w, func(c *dnsmasq.ConfFile) error {
		if p.Key == "" { // raw edit (comments, unknown syntax)
			return c.UpdateRawLine(idx, p.ExpectRaw, p.Raw)
		}
		if err := validKey(p.Key); err != nil {
			return err
		}
		if err := validValue(p.Value); err != nil {
			return err
		}
		if !p.Flag && strings.TrimSpace(p.Value) == "" {
			return fmt.Errorf("value is required")
		}
		return c.UpdateLine(idx, p.ExpectRaw, p.Key, p.Value, p.Flag)
	})
}

func (s *Server) apiDeleteLine(w http.ResponseWriter, r *http.Request) {
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil {
		jsonErr(w, 400, "invalid line index")
		return
	}
	expect := r.URL.Query().Get("expect_raw")
	s.mutateConf(w, func(c *dnsmasq.ConfFile) error {
		return c.DeleteLine(idx, expect)
	})
}

func (s *Server) apiSetScalar(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := decode(r, &p); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if err := validKey(p.Key); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if err := validValue(p.Value); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	s.mutateConf(w, func(c *dnsmasq.ConfFile) error {
		c.SetScalar(p.Key, p.Value)
		return nil
	})
}

func (s *Server) apiSetFlag(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Key string `json:"key"`
		On  bool   `json:"on"`
	}
	if err := decode(r, &p); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if err := validKey(p.Key); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	s.mutateConf(w, func(c *dnsmasq.ConfFile) error {
		c.SetFlag(p.Key, p.On)
		return nil
	})
}

func (s *Server) apiPutRaw(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Content string `json:"content"`
		Rev     string `json:"rev"`
	}
	if err := decode(r, &p); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if p.Content != "" && !strings.HasSuffix(p.Content, "\n") {
		p.Content += "\n"
	}
	s.confMu.Lock()
	defer s.confMu.Unlock()
	if p.Rev != "" {
		cur, err := dnsmasq.LoadConf(s.cfg.DnsmasqConf)
		if err != nil {
			jsonErr(w, 500, err.Error())
			return
		}
		if cur.Rev != p.Rev {
			jsonErr(w, 409, "the config changed since you loaded it — reload and re-apply your edit")
			return
		}
	}
	if err := s.writer.WriteRaw(p.Content); err != nil {
		if strings.Contains(err.Error(), "rejected by dnsmasq --test") {
			jsonErr(w, 422, err.Error())
			return
		}
		jsonErr(w, 500, err.Error())
		return
	}
	fresh, err := dnsmasq.LoadConf(s.cfg.DnsmasqConf)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	s.hub.Broadcast("config", map[string]string{"rev": fresh.Rev})
	jsonOK(w, map[string]string{"message": "Configuration saved", "rev": fresh.Rev})
}

func (s *Server) apiValidate(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Content string `json:"content"`
	}
	if err := decode(r, &p); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if err := s.writer.Validate(p.Content); err != nil {
		jsonOK(w, map[string]any{"valid": false, "error": err.Error()})
		return
	}
	jsonOK(w, map[string]any{"valid": true})
}

// ─── Service API ──────────────────────────────────────────────────────────

// statusPayload augments the systemd status with whether the on-disk config
// is newer than the running process (i.e. a restart is needed to apply it).
type statusPayload struct {
	dnsmasq.ServiceStatus
	StaleConfig bool `json:"stale_config"`
}

func (s *Server) currentStatus() *statusPayload {
	st, _ := s.svc.Status()
	if st == nil {
		st = &dnsmasq.ServiceStatus{Status: "unknown"}
	}
	p := &statusPayload{ServiceStatus: *st}
	if st.Running && !st.StartedAt.IsZero() {
		if fi, err := os.Stat(s.cfg.DnsmasqConf); err == nil {
			p.StaleConfig = fi.ModTime().After(st.StartedAt)
		}
	}
	return p
}

func (s *Server) apiServiceStatus(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, s.currentStatus())
}

func (s *Server) apiServiceAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	var err error
	var msg string
	switch action {
	case "start":
		err, msg = s.svc.Start(), "Service started"
	case "stop":
		err, msg = s.svc.Stop(), "Service stopped"
	case "restart":
		err, msg = s.svc.Restart(), "Service restarted"
	case "reload":
		err, msg = s.svc.Reload(), "Configuration reloaded (SIGHUP)"
	case "enable":
		err, msg = s.svc.Enable(), "Service enabled at boot"
	case "disable":
		err, msg = s.svc.Disable(), "Service disabled at boot"
	default:
		jsonErr(w, 404, "unknown action")
		return
	}
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	// Push the new state to every client immediately.
	go func() {
		time.Sleep(400 * time.Millisecond)
		s.hub.Broadcast("status", s.currentStatus())
	}()
	jsonOK(w, map[string]string{"message": msg})
}

func (s *Server) apiServiceLogs(w http.ResponseWriter, r *http.Request) {
	lines := 100
	if n, err := strconv.Atoi(r.URL.Query().Get("lines")); err == nil && n > 0 && n <= 2000 {
		lines = n
	}
	logs, err := s.svc.GetLogs(lines)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	jsonOK(w, map[string]any{"logs": logs})
}

// ─── Data API ─────────────────────────────────────────────────────────────

func (s *Server) apiGetLeases(w http.ResponseWriter, r *http.Request) {
	leases, err := dnsmasq.ParseLeases(s.cfg.DnsmasqLeases)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	jsonOK(w, leases)
}

func (s *Server) apiInterfaces(w http.ResponseWriter, r *http.Request) {
	ifaces, err := net.Interfaces()
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	type ifaceInfo struct {
		Name  string   `json:"name"`
		Up    bool     `json:"up"`
		Addrs []string `json:"addrs"`
	}
	out := []ifaceInfo{}
	for _, i := range ifaces {
		info := ifaceInfo{Name: i.Name, Up: i.Flags&net.FlagUp != 0, Addrs: []string{}}
		if addrs, err := i.Addrs(); err == nil {
			for _, a := range addrs {
				info.Addrs = append(info.Addrs, a.String())
			}
		}
		out = append(out, info)
	}
	jsonOK(w, out)
}

// apiLookup answers "does dnsmasq resolve this?" using the local server.
func (s *Server) apiLookup(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	qtype := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("type")))
	if name == "" {
		jsonErr(w, 400, "name is required")
		return
	}
	if qtype == "" {
		qtype = "A"
	}

	port := "53"
	if conf, err := dnsmasq.LoadConf(s.cfg.DnsmasqConf); err == nil {
		if v, ok := conf.Scalar("port"); ok && v != "" && v != "0" {
			port = v
		}
	}
	res := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 2 * time.Second}
			return d.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
		},
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	start := time.Now()
	var answers []string
	var err error
	switch qtype {
	case "A", "AAAA":
		var ips []net.IP
		ips, err = res.LookupIP(ctx, map[string]string{"A": "ip4", "AAAA": "ip6"}[qtype], name)
		for _, ip := range ips {
			answers = append(answers, ip.String())
		}
	case "CNAME":
		var c string
		c, err = res.LookupCNAME(ctx, name)
		if c != "" {
			answers = append(answers, c)
		}
	case "TXT":
		answers, err = res.LookupTXT(ctx, name)
	case "MX":
		var mxs []*net.MX
		mxs, err = res.LookupMX(ctx, name)
		for _, mx := range mxs {
			answers = append(answers, fmt.Sprintf("%d %s", mx.Pref, mx.Host))
		}
	case "NS":
		var nss []*net.NS
		nss, err = res.LookupNS(ctx, name)
		for _, ns := range nss {
			answers = append(answers, ns.Host)
		}
	case "PTR":
		answers, err = res.LookupAddr(ctx, name)
	case "SRV":
		var srvs []*net.SRV
		_, srvs, err = res.LookupSRV(ctx, "", "", name)
		for _, srv := range srvs {
			answers = append(answers, fmt.Sprintf("%d %d %d %s", srv.Priority, srv.Weight, srv.Port, srv.Target))
		}
	default:
		jsonErr(w, 400, "unsupported type: "+qtype)
		return
	}
	elapsed := time.Since(start)
	if err != nil {
		jsonOK(w, map[string]any{"ok": false, "error": err.Error(), "ms": elapsed.Milliseconds()})
		return
	}
	jsonOK(w, map[string]any{"ok": true, "answers": answers, "ms": elapsed.Milliseconds()})
}

// ─── Backups API ──────────────────────────────────────────────────────────

func (s *Server) apiGetBackups(w http.ResponseWriter, r *http.Request) {
	backups, err := s.writer.ListBackups()
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	jsonOK(w, backups)
}

func (s *Server) apiCreateBackup(w http.ResponseWriter, r *http.Request) {
	s.confMu.Lock()
	defer s.confMu.Unlock()
	if err := s.writer.CreateBackup(); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(201)
	jsonOK(w, map[string]string{"message": "Backup created"})
}

func (s *Server) apiReadBackup(w http.ResponseWriter, r *http.Request) {
	content, err := s.writer.ReadBackup(r.PathValue("name"))
	if err != nil {
		jsonErr(w, 404, err.Error())
		return
	}
	curBytes, _ := os.ReadFile(s.cfg.DnsmasqConf)
	jsonOK(w, map[string]string{"content": content, "current": string(curBytes)})
}

func (s *Server) apiRestoreBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Filename string `json:"filename"`
	}
	if err := decode(r, &req); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	s.confMu.Lock()
	err := s.writer.RestoreBackup(req.Filename)
	s.confMu.Unlock()
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	if fresh, err := dnsmasq.LoadConf(s.cfg.DnsmasqConf); err == nil {
		s.hub.Broadcast("config", map[string]string{"rev": fresh.Rev})
	}
	jsonOK(w, map[string]string{"message": "Backup restored"})
}

func (s *Server) apiDeleteBackup(w http.ResponseWriter, r *http.Request) {
	if err := s.writer.DeleteBackup(r.PathValue("name")); err != nil {
		jsonErr(w, 404, err.Error())
		return
	}
	jsonOK(w, map[string]string{"message": "Backup deleted"})
}
