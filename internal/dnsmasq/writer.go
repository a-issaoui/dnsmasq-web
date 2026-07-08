package dnsmasq

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Writer persists configuration content safely: every write validates the
// candidate config with `dnsmasq --test` (when available), backs up the
// current file, writes to a temp file and atomically renames it into place.
type Writer struct {
	confPath   string
	backupDir  string
	testBinary string // resolved dnsmasq binary for --test validation; "" disables
}

func NewWriter(confPath, backupDir string) *Writer {
	w := &Writer{confPath: confPath, backupDir: backupDir}
	if bin, err := exec.LookPath("dnsmasq"); err == nil {
		w.testBinary = bin
	}
	return w
}

// Validate runs `dnsmasq --test` against arbitrary config content without
// touching the live file. Returns nil when dnsmasq is unavailable.
func (w *Writer) Validate(content string) error {
	if w.testBinary == "" {
		return nil
	}
	tmp, err := os.CreateTemp("", "dnsmasq-web-test-*.conf")
	if err != nil {
		return fmt.Errorf("create validation temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	out, err := exec.Command(w.testBinary, "--test", "--conf-file="+tmp.Name()).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		msg = strings.ReplaceAll(msg, tmp.Name(), filepath.Base(w.confPath))
		return fmt.Errorf("config rejected by dnsmasq --test: %s", msg)
	}
	return nil
}

// WriteRaw validates, backs up and atomically installs new config content.
func (w *Writer) WriteRaw(content string) error {
	if err := w.Validate(content); err != nil {
		return err
	}
	if err := w.createBackup(); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}
	mode := os.FileMode(0644)
	if fi, err := os.Stat(w.confPath); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp := w.confPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), mode); err != nil {
		return err
	}
	return os.Rename(tmp, w.confPath)
}

func (w *Writer) createBackup() error {
	if err := os.MkdirAll(w.backupDir, 0755); err != nil {
		return err
	}
	data, err := os.ReadFile(w.confPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	ts := time.Now().Format("20060102-150405.000")
	return os.WriteFile(filepath.Join(w.backupDir, "dnsmasq.conf."+ts+".bak"), data, 0644)
}

// CreateBackup snapshots the current config on demand.
func (w *Writer) CreateBackup() error { return w.createBackup() }

// ListBackups returns available backups, newest first.
func (w *Writer) ListBackups() ([]BackupInfo, error) {
	entries, err := os.ReadDir(w.backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []BackupInfo{}, nil
		}
		return nil, err
	}
	out := []BackupInfo{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".bak") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, BackupInfo{
			Filename: e.Name(),
			Path:     filepath.Join(w.backupDir, e.Name()),
			Size:     info.Size(),
			ModTime:  info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	return out, nil
}

// backupPath resolves a backup filename safely inside the backup directory.
func (w *Writer) backupPath(filename string) (string, error) {
	filename = filepath.Base(filename)
	if !strings.HasSuffix(filename, ".bak") {
		return "", fmt.Errorf("invalid backup filename")
	}
	p := filepath.Join(w.backupDir, filename)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("backup not found: %s", filename)
	}
	return p, nil
}

// ReadBackup returns the content of a backup for preview/diff.
func (w *Writer) ReadBackup(filename string) (string, error) {
	p, err := w.backupPath(filename)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	return string(data), err
}

// RestoreBackup replaces the live config with a backup (validated, and the
// pre-restore state is itself backed up first).
func (w *Writer) RestoreBackup(filename string) error {
	p, err := w.backupPath(filename)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return w.WriteRaw(string(data))
}

// DeleteBackup removes a backup file.
func (w *Writer) DeleteBackup(filename string) error {
	p, err := w.backupPath(filename)
	if err != nil {
		return err
	}
	return os.Remove(p)
}
