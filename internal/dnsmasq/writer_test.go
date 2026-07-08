package dnsmasq

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubDnsmasq puts a fake `dnsmasq` binary on PATH whose --test exits 0 or 1,
// so the full validate → backup → atomic-rename pipeline runs without the
// real daemon. NewWriter resolves the binary via exec.LookPath at
// construction, so the stub must be installed first.
func stubDnsmasq(t *testing.T, ok bool) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nexit 0\n"
	if !ok {
		script = "#!/bin/sh\necho 'dnsmasq: bad option at line 3 of test.conf' >&2\nexit 1\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "dnsmasq"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func newTestWriter(t *testing.T, valid bool) (*Writer, string, string) {
	t.Helper()
	stubDnsmasq(t, valid)
	dir := t.TempDir()
	confPath := filepath.Join(dir, "dnsmasq.conf")
	backupDir := filepath.Join(dir, "backups")
	if err := os.WriteFile(confPath, []byte("cache-size=1000\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return NewWriter(confPath, backupDir), confPath, backupDir
}

func countBackups(t *testing.T, w *Writer) int {
	t.Helper()
	list, err := w.ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

func TestWriteRawHappyPathBacksUpThenSwaps(t *testing.T) {
	w, confPath, _ := newTestWriter(t, true)

	if err := w.WriteRaw("cache-size=2000\n"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(confPath)
	if string(data) != "cache-size=2000\n" {
		t.Fatalf("config not written, got %q", data)
	}
	if n := countBackups(t, w); n != 1 {
		t.Fatalf("want exactly 1 backup of the pre-write state, got %d", n)
	}
	content, err := w.ReadBackup(mustFirstBackup(t, w))
	if err != nil {
		t.Fatal(err)
	}
	if content != "cache-size=1000\n" {
		t.Fatalf("backup must hold the previous content, got %q", content)
	}
	if _, err := os.Stat(confPath + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file must not linger after rename")
	}
}

func TestWriteRawRejectedConfigTouchesNothing(t *testing.T) {
	w, confPath, _ := newTestWriter(t, false)

	err := w.WriteRaw("garbage\n")
	if err == nil {
		t.Fatal("invalid config must be rejected")
	}
	if !strings.Contains(err.Error(), "rejected by dnsmasq --test") {
		t.Fatalf("error must carry the dnsmasq message, got %v", err)
	}
	data, _ := os.ReadFile(confPath)
	if string(data) != "cache-size=1000\n" {
		t.Fatalf("live config must be untouched after rejection, got %q", data)
	}
	if n := countBackups(t, w); n != 0 {
		t.Fatalf("no backup should be made for a rejected write, got %d", n)
	}
}

func TestWriteRawPreservesFileMode(t *testing.T) {
	w, confPath, _ := newTestWriter(t, true)
	if err := os.Chmod(confPath, 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteRaw("port=53\n"); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(confPath)
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("mode must survive rewrite, got %o", fi.Mode().Perm())
	}
}

func TestValidateWithoutDnsmasqIsNoop(t *testing.T) {
	// empty PATH → LookPath fails → validation disabled by design
	t.Setenv("PATH", t.TempDir())
	w := NewWriter(filepath.Join(t.TempDir(), "c.conf"), t.TempDir())
	if err := w.Validate("anything at all"); err != nil {
		t.Fatalf("validation must be a no-op without dnsmasq, got %v", err)
	}
}

func TestRestoreBackupRoundTrip(t *testing.T) {
	w, confPath, _ := newTestWriter(t, true)
	if err := w.WriteRaw("cache-size=2000\n"); err != nil {
		t.Fatal(err)
	}
	name := mustFirstBackup(t, w) // holds cache-size=1000

	if err := w.RestoreBackup(name); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(confPath)
	if string(data) != "cache-size=1000\n" {
		t.Fatalf("restore must bring back the snapshot, got %q", data)
	}
	// the pre-restore state must itself have been snapshotted
	if n := countBackups(t, w); n != 2 {
		t.Fatalf("restore must snapshot the pre-restore state, got %d backups", n)
	}
}

func TestBackupPathTraversalIsRejected(t *testing.T) {
	w, _, backupDir := newTestWriter(t, true)
	if err := w.CreateBackup(); err != nil {
		t.Fatal(err)
	}
	for _, evil := range []string{
		"../../../etc/passwd",
		"../dnsmasq.conf.bak",
		"nothing.txt",
		"",
	} {
		if _, err := w.ReadBackup(evil); err == nil {
			t.Errorf("ReadBackup(%q) must fail", evil)
		}
		if err := w.DeleteBackup(evil); err == nil {
			t.Errorf("DeleteBackup(%q) must fail", evil)
		}
	}
	// the traversal attempts must not have deleted anything real
	entries, _ := os.ReadDir(backupDir)
	if len(entries) != 1 {
		t.Fatalf("backup dir tampered with, %d entries", len(entries))
	}
}

func TestListBackupsNewestFirstAndIgnoresJunk(t *testing.T) {
	w, _, backupDir := newTestWriter(t, true)
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		t.Fatal(err)
	}
	junk := []string{"README.txt", "not-a-backup.conf"}
	for _, f := range junk {
		if err := os.WriteFile(filepath.Join(backupDir, f), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	old := filepath.Join(backupDir, "dnsmasq.conf.20200101-000000.000.bak")
	if err := os.WriteFile(old, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := w.CreateBackup(); err != nil {
		t.Fatal(err)
	}

	list, err := w.ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 backups (junk ignored), got %d", len(list))
	}
	if !list[0].ModTime.After(list[1].ModTime) && !list[0].ModTime.Equal(list[1].ModTime) {
		t.Fatal("backups must be sorted newest first")
	}
}

func mustFirstBackup(t *testing.T, w *Writer) string {
	t.Helper()
	list, err := w.ListBackups()
	if err != nil || len(list) == 0 {
		t.Fatalf("expected a backup, err=%v n=%d", err, len(list))
	}
	return list[len(list)-1].Filename // oldest
}
