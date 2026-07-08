package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// newTestServer spins up the real route stack against throwaway dirs and
// minimal templates. mod tweaks the config (auth, tokens) before startup.
func newTestServer(t *testing.T, mod func(*Config)) *httptest.Server {
	t.Helper()
	dir := t.TempDir()

	tmpl := filepath.Join(dir, "templates")
	static := filepath.Join(dir, "static")
	for _, d := range []string{tmpl, static} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	pages := map[string]string{
		"base.html":  `<title>{{.Title}}</title>{{if .AuthEnabled}}<button id="logout-btn"></button>{{end}}{{template "content" .}}`,
		"index.html": `{{define "content"}}dashboard{{end}}`,
		"login.html": `<form id="login-form">login-page</form>`,
	}
	for name, content := range pages {
		if err := os.WriteFile(filepath.Join(tmpl, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(static, "style.css"), []byte("body{}"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		DnsmasqConf:   filepath.Join(dir, "dnsmasq.conf"),
		DnsmasqLeases: filepath.Join(dir, "dnsmasq.leases"),
		BackupDir:     filepath.Join(dir, "backups"),
		TemplateDir:   tmpl,
		StaticDir:     static,
	}
	if err := os.WriteFile(cfg.DnsmasqConf, []byte("cache-size=1000\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if mod != nil {
		mod(cfg)
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.SetupRoutes())
	t.Cleanup(ts.Close)
	return ts
}

// noRedirects returns a client that surfaces 3xx instead of following them
// and carries no cookie jar unless one is attached by the caller.
func noRedirects() *http.Client {
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func doJSON(t *testing.T, c *http.Client, method, url string, body any, hdr map[string]string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}
