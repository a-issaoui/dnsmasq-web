package api

import (
	"bufio"
	"net/http"
	"os"
	"regexp"
	"strings"

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

var reCheckName = regexp.MustCompile(`^browser-check-[a-z0-9]{6,32}\.test$`)

// GET /api/resolver-check/verify?name= — did a query for the marker name
// reach dnsmasq? The client makes the browser resolve the name first; if the
// browser uses its own DoH, the query never appears in dnsmasq's journal.
func (s *Server) apiResolverVerify(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("name")))
	if !reCheckName.MatchString(name) {
		jsonErr(w, 400, "invalid check name")
		return
	}
	logs, err := s.svc.GetLogs(400)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	seen := false
	for i := len(logs) - 1; i >= 0 && i >= len(logs)-400; i-- {
		l := logs[i]
		if strings.Contains(l, name) && strings.Contains(l, "query[") {
			seen = true
			break
		}
	}
	jsonOK(w, map[string]any{"seen": seen, "name": name})
}
