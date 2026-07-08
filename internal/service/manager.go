package service

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"dnsmasq-web/internal/dnsmasq"
)

type Manager struct {
	name       string
	scriptPath string

	versionOnce sync.Once
	version     string
}

func New(name string) *Manager {
	if name == "" {
		name = "dnsmasq"
	}
	return &Manager{name: name, scriptPath: findScript()}
}

func findScript() string {
	if exePath, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exePath), "scripts", "dnsmasq-manager.sh")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if _, err := os.Stat("./scripts/dnsmasq-manager.sh"); err == nil {
		p, _ := filepath.Abs("./scripts/dnsmasq-manager.sh")
		return p
	}
	for _, p := range []string{
		"/opt/dnsmasq-web/scripts/dnsmasq-manager.sh",
		"/usr/local/share/dnsmasq-web/scripts/dnsmasq-manager.sh",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "./scripts/dnsmasq-manager.sh"
}

// Version returns the installed dnsmasq version string (cached).
func (m *Manager) Version() string {
	m.versionOnce.Do(func() {
		out, err := exec.Command("dnsmasq", "--version").Output()
		if err != nil {
			return
		}
		first, _, _ := strings.Cut(string(out), "\n")
		m.version = strings.TrimSpace(strings.TrimPrefix(first, "Dnsmasq version "))
		if i := strings.Index(m.version, " "); i > 0 {
			m.version = m.version[:i]
		}
	})
	return m.version
}

func (m *Manager) Status() (*dnsmasq.ServiceStatus, error) {
	out, _ := exec.Command("systemctl", "is-active", m.name).CombinedOutput()
	active := strings.TrimSpace(string(out)) == "active"

	st := &dnsmasq.ServiceStatus{Running: active, Active: active, Version: m.Version()}
	st.Enabled, _ = m.IsEnabled()

	show, err := exec.Command("systemctl", "show", m.name,
		"--property=ActiveState,SubState,MainPID,ExecMainStartTimestamp").CombinedOutput()
	if err != nil {
		st.Status = "unknown"
		return st, nil
	}
	props := parseProps(string(show))
	st.Status = props["SubState"]
	if pid, e := strconv.Atoi(props["MainPID"]); e == nil && pid > 0 {
		st.PID = pid
		st.MemoryMB = getMemory(pid)
	}
	if ts := props["ExecMainStartTimestamp"]; ts != "" && active {
		if t := parseSystemdTime(ts); !t.IsZero() {
			st.StartedAt = t
			st.Uptime = humanDuration(time.Since(t))
		}
	}
	return st, nil
}

func (m *Manager) Start() error   { return m.runScript("start") }
func (m *Manager) Stop() error    { return m.runScript("stop") }
func (m *Manager) Restart() error { return m.runScript("restart") }
func (m *Manager) Reload() error  { return m.runScript("reload") }
func (m *Manager) Enable() error  { return m.runSystemctl("enable") }
func (m *Manager) Disable() error { return m.runSystemctl("disable") }

func (m *Manager) IsEnabled() (bool, error) {
	out, err := exec.Command("systemctl", "is-enabled", m.name).CombinedOutput()
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(string(out)) == "enabled", nil
}

// GetLogs returns the last n journal lines for the unit.
func (m *Manager) GetLogs(lines int) ([]string, error) {
	if lines <= 0 {
		lines = 100
	}
	out, err := exec.Command("journalctl", "-u", m.name, "-n", strconv.Itoa(lines),
		"--no-pager", "--output=short-iso").CombinedOutput()
	if err != nil {
		return m.getLogsFromSyslog(lines), nil
	}
	logs := []string{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		if l := sc.Text(); !strings.HasPrefix(l, "-- ") {
			logs = append(logs, l)
		}
	}
	return logs, nil
}

func (m *Manager) getLogsFromSyslog(lines int) []string {
	out, err := exec.Command("tail", "-n", "2000", "/var/log/syslog").CombinedOutput()
	if err != nil {
		return []string{"Log unavailable: " + err.Error()}
	}
	logs := []string{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		if line := sc.Text(); strings.Contains(line, "dnsmasq") {
			logs = append(logs, line)
		}
	}
	if len(logs) > lines {
		logs = logs[len(logs)-lines:]
	}
	return logs
}

// FollowJournal streams new journal lines for the unit until ctx is
// cancelled. The returned channel closes when the underlying journalctl
// process exits.
func (m *Manager) FollowJournal(ctx context.Context) (<-chan string, error) {
	cmd := exec.CommandContext(ctx, "journalctl", "-u", m.name, "-f", "-n", "0",
		"--no-pager", "--output=short-iso")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("journalctl -f: %w", err)
	}
	ch := make(chan string, 256)
	go func() {
		defer close(ch)
		defer cmd.Wait()
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "-- ") {
				continue
			}
			select {
			case ch <- line:
			case <-ctx.Done():
				return
			default: // drop rather than block if a consumer stalls
			}
		}
	}()
	return ch, nil
}

func (m *Manager) runScript(action string) error {
	out, err := exec.Command("sudo", "-n", "bash", m.scriptPath, action).CombinedOutput()
	if err != nil {
		return fmt.Errorf("dnsmasq-manager %s: %w\n%s", action, err, string(out))
	}
	return nil
}

func (m *Manager) runSystemctl(action string) error {
	out, err := exec.Command("sudo", "-n", "systemctl", action, m.name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s %s: %w\n%s", action, m.name, err, string(out))
	}
	return nil
}

func parseProps(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		if idx := strings.Index(line, "="); idx >= 0 {
			out[line[:idx]] = line[idx+1:]
		}
	}
	return out
}

func getMemory(pid int) float64 {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseFloat(fields[1], 64)
				return kb / 1024
			}
		}
	}
	return 0
}

func parseSystemdTime(ts string) time.Time {
	layouts := []string{
		"Mon 2006-01-02 15:04:05 MST",
		time.RFC3339,
		"2006-01-02 15:04:05.999999999 -0700 MST",
	}
	for _, l := range layouts {
		if parsed, err := time.ParseInLocation(l, ts, time.Local); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func humanDuration(d time.Duration) string {
	h := int(d.Hours())
	mi := int(d.Minutes()) % 60
	if h >= 24 {
		return fmt.Sprintf("%dd %dh %dm", h/24, h%24, mi)
	}
	if h == 0 && mi == 0 {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dh %dm", h, mi)
}
