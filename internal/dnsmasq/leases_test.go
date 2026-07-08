package dnsmasq

import (
	"os"
	"path/filepath"
	"testing"
)

func writeLeases(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "dnsmasq.leases")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseLeasesMissingFileMeansNoLeases(t *testing.T) {
	leases, err := ParseLeases(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(leases) != 0 {
		t.Fatalf("missing file: want empty, got %d leases err=%v", len(leases), err)
	}
}

func TestParseLeasesV4AndV6(t *testing.T) {
	content := `duid 00:01:00:01:2b:xx:yy:zz
1893456000 aa:bb:cc:dd:ee:ff 192.168.1.50 nas 01:aa:bb:cc:dd:ee:ff
1893456000 11:22:33:44:55:66 192.168.1.51 * *
1893456000 123456789 fd00::5 laptop 00:03:00:01:aa:bb:cc:dd:ee:ff
garbage line that should be skipped
notanumber aa:bb:cc:dd:ee:ff 192.168.1.99 x *
`
	leases, err := ParseLeases(writeLeases(t, content))
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 3 {
		t.Fatalf("want 3 leases (duid header + junk skipped), got %d", len(leases))
	}

	nas := leases[0]
	if nas.Hostname != "nas" || nas.IPAddress != "192.168.1.50" ||
		nas.MACAddress != "aa:bb:cc:dd:ee:ff" || nas.ClientID != "01:aa:bb:cc:dd:ee:ff" || nas.IPv6 {
		t.Fatalf("v4 lease parsed wrong: %+v", nas)
	}

	anon := leases[1]
	if anon.Hostname != "" || anon.ClientID != "" {
		t.Fatalf("'*' placeholders must map to empty strings: %+v", anon)
	}

	v6 := leases[2]
	if !v6.IPv6 || v6.IPAddress != "fd00::5" || v6.Hostname != "laptop" {
		t.Fatalf("v6 lease parsed wrong: %+v", v6)
	}

	if nas.ExpiryUnix != 1893456000 || nas.Expiry.Unix() != 1893456000 {
		t.Fatalf("expiry mismatch: %+v", nas)
	}
}

func TestParseLeasesEmptyFile(t *testing.T) {
	leases, err := ParseLeases(writeLeases(t, ""))
	if err != nil || len(leases) != 0 {
		t.Fatalf("empty file: want no leases, got %d err=%v", len(leases), err)
	}
}
