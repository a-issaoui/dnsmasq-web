package api

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"dnsmasq-web/internal/dnsmasq"
)

// Resolver health: is this machine — and, critically, the browser — actually
// using dnsmasq? Browsers can silently auto-upgrade to their own DoH and
// bypass the whole chain; the check below detects that from the outside.

// dohUpgradable are public resolvers browsers recognise and auto-upgrade to
// their DoH endpoints when seen in the system resolver config.
var dohUpgradable = map[string]string{
	"1.1.1.1": "Cloudflare", "1.0.0.1": "Cloudflare",
	"8.8.8.8": "Google", "8.8.4.4": "Google",
	"9.9.9.9": "Quad9", "149.112.112.112": "Quad9",
	"94.140.14.14": "AdGuard", "94.140.15.15": "AdGuard",
	"208.67.222.222": "OpenDNS", "208.67.220.220": "OpenDNS",
}

func readResolvConf() []string {
	f, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(strings.TrimSpace(sc.Text()))
		if len(fields) >= 2 && fields[0] == "nameserver" {
			out = append(out, fields[1])
		}
	}
	return out
}

// GET /api/resolver-check — passive state for the health card.
func (s *Server) apiResolverCheck(w http.ResponseWriter, r *http.Request) {
	ns := readResolvConf()
	dnsmasqFirst := len(ns) > 0 && (ns[0] == "127.0.0.1" || ns[0] == "::1")

	type risk struct {
		IP       string `json:"ip"`
		Provider string `json:"provider"`
	}
	risks := []risk{}
	for _, ip := range ns {
		if p, ok := dohUpgradable[ip]; ok {
			risks = append(risks, risk{ip, p})
		}
	}

	logQueries := false
	if conf, err := dnsmasq.LoadConf(s.cfg.DnsmasqConf); err == nil {
		_, logQueries = conf.Scalar("log-queries")
	}

	jsonOK(w, map[string]any{
		"nameservers":   ns,
		"dnsmasq_first": dnsmasqFirst,
		"doh_risks":     risks,
		"log_queries":   logQueries,
	})
}

var reCheckName = regexp.MustCompile(`^(browser|control)-check-[a-z0-9]{6,32}\.test$`)

// localProbe makes the server itself resolve a name through dnsmasq. Used as
// a control: if the control query shows up in the journal but the browser's
// marker doesn't, the browser is genuinely bypassing — not a logging hiccup.
func (s *Server) localProbe(name string) {
	port := "53"
	if conf, err := dnsmasq.LoadConf(s.cfg.DnsmasqConf); err == nil {
		if v, ok := conf.Scalar("port"); ok && v != "" && v != "0" {
			port = v
		}
	}
	res := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: time.Second}
			return d.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_, _ = res.LookupHost(ctx, name) // NXDOMAIN expected — only the journal entry matters
}

// journalSeen reports which of the given names appear in a recent dnsmasq
// query[...] journal line (any record type: A, AAAA, HTTPS/65, …).
func (s *Server) journalSeen(names ...string) (map[string]bool, error) {
	logs, err := s.svc.GetLogs(500)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(names))
	for _, l := range logs {
		if !strings.Contains(l, "query[") {
			continue
		}
		for _, n := range names {
			if !out[n] && strings.Contains(l, n) {
				out[n] = true
			}
		}
	}
	return out, nil
}

// GET /api/resolver-check/verify?name=&control=[&fire=1]
//
// The client makes the BROWSER resolve `name`; the server resolves `control`
// itself (once, when fire=1). The client polls this endpoint and derives a
// tri-state verdict:
//
//	marker seen              → browser is on the chain
//	control seen, marker not → browser bypasses dnsmasq (its own DoH)
//	neither seen             → inconclusive (journald latency / logging issue)
func (s *Server) apiResolverVerify(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("name")))
	control := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("control")))
	if !reCheckName.MatchString(name) || (control != "" && !reCheckName.MatchString(control)) {
		jsonErr(w, 400, "invalid check name")
		return
	}
	if r.URL.Query().Get("fire") == "1" && control != "" {
		s.localProbe(control)
	}
	names := []string{name}
	if control != "" {
		names = append(names, control)
	}
	seen, err := s.journalSeen(names...)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	jsonOK(w, map[string]any{
		"marker_seen":  seen[name],
		"control_seen": control != "" && seen[control],
	})
}
