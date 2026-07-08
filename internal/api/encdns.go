package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"dnsmasq-web/internal/dnsmasq"
)

// Encrypted upstream: dnsmasq forwards to a local dnscrypt-proxy instance
// which carries queries upstream over DoH/DNSCrypt. dnsmasq keeps doing the
// caching, local records and .lan conditional forwarding; only the internet
// hop is encrypted.

const (
	encUnit     = "dnscrypt-proxy"
	encToml     = "/etc/dnscrypt-proxy/dnscrypt-proxy.toml"
	encAddr     = "127.0.0.1:5053"
	encServerLn = "127.0.0.1#5053" // dnsmasq server= syntax
)

type encProvider struct {
	Label       string   `json:"label"`
	ServerNames []string `json:"-"` // dnscrypt-proxy resolver names
	PlainDNS    []string `json:"-"` // fallback used when disabling
}

var encProviders = map[string]encProvider{
	"cloudflare": {"Cloudflare (DoH)", []string{"cloudflare"}, []string{"1.1.1.1", "1.0.0.1"}},
	"quad9":      {"Quad9 (DoH, filtered)", []string{"quad9-doh-ip4-port443-filter-pri"}, []string{"9.9.9.9", "149.112.112.112"}},
	"google":     {"Google (DoH)", []string{"google"}, []string{"8.8.8.8", "8.8.4.4"}},
}

// ─── status ───────────────────────────────────────────────────────────

type encStatus struct {
	Installed        bool   `json:"installed"`
	Active           bool   `json:"active"`
	Enabled          bool   `json:"enabled"` // unit enabled at boot
	Answering        bool   `json:"answering"`
	Protocol         string `json:"protocol,omitempty"` // e.g. "DoH", from forwarder logs
	Provider         string `json:"provider,omitempty"`
	DnsmasqEncrypted bool   `json:"dnsmasq_encrypted"`
	Detail           string `json:"detail,omitempty"`
}

func (s *Server) encDNSStatus() *encStatus {
	st := &encStatus{}
	if _, err := exec.LookPath("dnscrypt-proxy"); err == nil {
		st.Installed = true
	} else if _, err := os.Stat(encToml); err == nil {
		st.Installed = true
	}
	if !st.Installed {
		st.Detail = "dnscrypt-proxy is not installed"
		return st
	}

	out, _ := exec.Command("systemctl", "is-active", encUnit).CombinedOutput()
	st.Active = strings.TrimSpace(string(out)) == "active"
	out, _ = exec.Command("systemctl", "is-enabled", encUnit).CombinedOutput()
	st.Enabled = strings.TrimSpace(string(out)) == "enabled"

	st.Provider = currentEncProvider()

	if st.Active {
		st.Answering = encProbe()
		st.Protocol = encProtocolFromLogs()
	}

	if conf, err := dnsmasq.LoadConf(s.cfg.DnsmasqConf); err == nil {
		for _, l := range conf.Entries("server") {
			if l.Value == encServerLn {
				st.DnsmasqEncrypted = true
				break
			}
		}
	}
	return st
}

// encProbe performs a real DNS lookup through the forwarder.
func encProbe() bool {
	res := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 1500 * time.Millisecond}
			return d.DialContext(ctx, network, encAddr)
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	addrs, err := res.LookupHost(ctx, "example.com")
	return err == nil && len(addrs) > 0
}

var reEncOK = regexp.MustCompile(`\[[\w-]+\] OK \(([^)]+)\)`)

func encProtocolFromLogs() string {
	out, err := exec.Command("journalctl", "-u", encUnit, "-n", "80", "--no-pager", "-o", "cat").Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(string(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if m := reEncOK.FindStringSubmatch(lines[i]); m != nil {
			return m[1]
		}
	}
	return ""
}

func currentEncProvider() string {
	data, err := os.ReadFile(encToml)
	if err != nil {
		return ""
	}
	m := regexp.MustCompile(`(?m)^server_names *= *\[([^\]]*)\]`).FindSubmatch(data)
	if m == nil {
		return ""
	}
	names := string(m[1])
	for id, p := range encProviders {
		for _, n := range p.ServerNames {
			if strings.Contains(names, "'"+n+"'") || strings.Contains(names, `"`+n+`"`) {
				return id
			}
		}
	}
	return "custom"
}

// ─── toml management ──────────────────────────────────────────────────

func setTomlKey(content, key, value string) string {
	reSet := regexp.MustCompile(`(?m)^` + key + ` *=.*$`)
	if reSet.MatchString(content) {
		return reSet.ReplaceAllString(content, key+" = "+value)
	}
	reComment := regexp.MustCompile(`(?m)^# *` + key + ` *=.*$`)
	if loc := reComment.FindStringIndex(content); loc != nil {
		return content[:loc[0]] + key + " = " + value + content[loc[1]:]
	}
	// top-level keys must precede any [section]: insert at the very top
	return key + " = " + value + "\n" + content
}

func writeEncConfig(provider encProvider) error {
	data, err := os.ReadFile(encToml)
	if err != nil {
		return fmt.Errorf("read %s: %w", encToml, err)
	}
	content := string(data)
	quoted := make([]string, len(provider.ServerNames))
	for i, n := range provider.ServerNames {
		quoted[i] = "'" + n + "'"
	}
	content = setTomlKey(content, "server_names", "["+strings.Join(quoted, ", ")+"]")
	content = setTomlKey(content, "listen_addresses", "['"+encAddr+"']")
	content = setTomlKey(content, "cache", "false") // dnsmasq is the cache
	tmp := encToml + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, encToml)
}

func encSystemctl(args ...string) error {
	out, err := exec.Command("sudo", append([]string{"-n", "systemctl"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %v — %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ─── handlers ─────────────────────────────────────────────────────────

func (s *Server) apiEncDNSStatus(w http.ResponseWriter, r *http.Request) {
	providers := map[string]string{}
	for id, p := range encProviders {
		providers[id] = p.Label
	}
	jsonOK(w, map[string]any{"status": s.encDNSStatus(), "providers": providers})
}

func (s *Server) apiEncDNSSet(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Enabled  bool   `json:"enabled"`
		Provider string `json:"provider"`
	}
	if err := decode(r, &p); err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if p.Provider == "" {
		p.Provider = "cloudflare"
	}
	prov, ok := encProviders[p.Provider]
	if !ok {
		jsonErr(w, 400, "unknown provider: "+p.Provider)
		return
	}

	if p.Enabled {
		if err := s.encEnable(prov); err != nil {
			jsonErr(w, 500, err.Error())
			return
		}
	} else {
		if err := s.encDisable(prov); err != nil {
			jsonErr(w, 500, err.Error())
			return
		}
	}

	if fresh, err := dnsmasq.LoadConf(s.cfg.DnsmasqConf); err == nil {
		s.hub.Broadcast("config", map[string]string{"rev": fresh.Rev})
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		s.hub.Broadcast("status", s.currentStatus())
	}()
	jsonOK(w, map[string]any{
		"message": map[bool]string{true: "Encrypted upstream enabled — internet DNS now leaves over HTTPS", false: "Encrypted upstream disabled — using plain DNS upstreams"}[p.Enabled],
		"status":  s.encDNSStatus(),
	})
}

func (s *Server) encEnable(prov encProvider) error {
	st := s.encDNSStatus()
	if !st.Installed {
		return fmt.Errorf("dnscrypt-proxy is not installed — run: sudo dnf install dnscrypt-proxy (or apt install dnscrypt-proxy)")
	}
	if err := writeEncConfig(prov); err != nil {
		return err
	}
	if err := encSystemctl("restart", encUnit); err != nil {
		return err
	}
	if err := encSystemctl("enable", encUnit); err != nil {
		return err
	}
	// wait for the forwarder to actually answer before pointing dnsmasq at it
	deadline := time.Now().Add(12 * time.Second)
	for !encProbe() {
		if time.Now().After(deadline) {
			return fmt.Errorf("dnscrypt-proxy started but is not answering on %s — check: journalctl -u dnscrypt-proxy", encAddr)
		}
		time.Sleep(700 * time.Millisecond)
	}
	if err := s.encRewriteDnsmasq(true, prov); err != nil {
		return err
	}
	return s.svc.Restart()
}

func (s *Server) encDisable(prov encProvider) error {
	if err := s.encRewriteDnsmasq(false, prov); err != nil {
		return err
	}
	if err := s.svc.Restart(); err != nil {
		return err
	}
	// stop the forwarder only after dnsmasq no longer depends on it
	if err := encSystemctl("disable", "--now", encUnit); err != nil {
		return err
	}
	return nil
}

// encRewriteDnsmasq swaps the *global* upstream servers while preserving all
// domain-scoped entries (server=/lan/… conditional forwarding stays intact).
func (s *Server) encRewriteDnsmasq(encrypted bool, prov encProvider) error {
	s.confMu.Lock()
	defer s.confMu.Unlock()

	conf, err := dnsmasq.LoadConf(s.cfg.DnsmasqConf)
	if err != nil {
		return err
	}
	// remove global (non domain-scoped) server= lines, back to front so
	// indices stay valid
	for i := len(conf.Lines) - 1; i >= 0; i-- {
		l := conf.Lines[i]
		if l.Key == "server" && !strings.HasPrefix(l.Value, "/") {
			if err := conf.DeleteLine(l.Idx, l.Raw); err != nil {
				return err
			}
		}
	}
	if encrypted {
		conf.AddLine("server", encServerLn, false)
	} else {
		for _, ip := range prov.PlainDNS {
			conf.AddLine("server", ip, false)
		}
	}
	return s.writer.WriteRaw(conf.Serialize())
}
