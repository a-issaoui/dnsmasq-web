package dnsmasq

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sample exercises every line shape the parser must preserve: comments,
// blanks, flags, scalars, multi-values, odd spacing and unknown directives.
const sample = `# dnsmasq configuration
# managed partly by hand — this comment must survive everything

domain-needed
bogus-priv
cache-size=1000

# upstream
server=1.1.1.1
server=/lan/192.168.1.1
  no-resolv

unknown-directive=kept-verbatim
weird = spaced value
`

func loadSample(t *testing.T) *ConfFile {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dnsmasq.conf")
	if err := os.WriteFile(path, []byte(sample), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConf(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLoadConfMissingFileIsEmptyButWritable(t *testing.T) {
	c, err := LoadConf(filepath.Join(t.TempDir(), "nope.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Lines) != 0 || c.Rev == "" {
		t.Fatalf("want empty conf with rev, got %d lines rev=%q", len(c.Lines), c.Rev)
	}
}

func TestSerializeRoundTripsByteForByte(t *testing.T) {
	c := loadSample(t)
	if got := c.Serialize(); got != sample {
		t.Fatalf("round-trip changed content:\n--- want ---\n%q\n--- got ---\n%q", sample, got)
	}
}

func TestParseLineShapes(t *testing.T) {
	tests := []struct {
		raw   string
		key   string
		value string
		flag  bool
	}{
		{"cache-size=1000", "cache-size", "1000", false},
		{"dnssec", "dnssec", "", true},
		{"  no-resolv", "no-resolv", "", true},
		{"weird = spaced value", "weird", "spaced value", false},
		{"server=/lan/192.168.1.1", "server", "/lan/192.168.1.1", false},
		{"# a comment", "", "", false},
		{"", "", "", false},
		{"   ", "", "", false},
		{"address=/ads.example/", "address", "/ads.example/", false},
	}
	for _, tt := range tests {
		l := parseLine(0, tt.raw)
		if l.Key != tt.key || l.Value != tt.value || l.Flag != tt.flag {
			t.Errorf("parseLine(%q) = {key:%q value:%q flag:%v}, want {key:%q value:%q flag:%v}",
				tt.raw, l.Key, l.Value, l.Flag, tt.key, tt.value, tt.flag)
		}
	}
}

func TestAddLineGroupsAfterLastSibling(t *testing.T) {
	c := loadSample(t)
	added := c.AddLine("server", "9.9.9.9", false)

	entries := c.Entries("server")
	if len(entries) != 3 {
		t.Fatalf("want 3 server lines, got %d", len(entries))
	}
	if entries[2].Raw != "server=9.9.9.9" || entries[2].Idx != added.Idx {
		t.Fatalf("new server line misplaced: %+v", entries[2])
	}
	// it must sit directly after the previous last server line
	if c.Lines[added.Idx-1].Raw != "server=/lan/192.168.1.1" {
		t.Fatalf("expected insertion after last sibling, line before is %q", c.Lines[added.Idx-1].Raw)
	}
	// everything else untouched
	if !strings.Contains(c.Serialize(), "# managed partly by hand — this comment must survive everything") {
		t.Fatal("comment lost after AddLine")
	}
}

func TestAddLineNewKeyAppendsAtEnd(t *testing.T) {
	c := loadSample(t)
	c.AddLine("dhcp-range", "192.168.1.50,192.168.1.150,12h", false)
	last := c.Lines[len(c.Lines)-1]
	if last.Raw != "dhcp-range=192.168.1.50,192.168.1.150,12h" {
		t.Fatalf("want new key appended at end, got %q", last.Raw)
	}
}

func TestUpdateLineGuardsWithExpectRaw(t *testing.T) {
	c := loadSample(t)
	idx := c.Entries("cache-size")[0].Idx

	// stale expectation → ConflictError, nothing changed
	err := c.UpdateLine(idx, "cache-size=9999", "cache-size", "2000", false)
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("want ConflictError, got %v", err)
	}
	if got, _ := c.Scalar("cache-size"); got != "1000" {
		t.Fatalf("conflicting update must not apply, cache-size=%q", got)
	}

	// correct expectation → applied
	if err := c.UpdateLine(idx, "cache-size=1000", "cache-size", "2000", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Scalar("cache-size"); got != "2000" {
		t.Fatalf("update not applied, cache-size=%q", got)
	}
}

func TestUpdateRawLineRejectsNewlines(t *testing.T) {
	c := loadSample(t)
	if err := c.UpdateRawLine(0, "", "# ok\ncache-size=1"); err == nil {
		t.Fatal("newline injection must be rejected")
	}
}

func TestDeleteLinePreservesEverythingElse(t *testing.T) {
	c := loadSample(t)
	before := len(c.Lines)
	idx := c.Entries("bogus-priv")[0].Idx
	if err := c.DeleteLine(idx, "bogus-priv"); err != nil {
		t.Fatal(err)
	}
	if len(c.Lines) != before-1 {
		t.Fatalf("want %d lines, got %d", before-1, len(c.Lines))
	}
	out := c.Serialize()
	if strings.Contains(out, "bogus-priv") {
		t.Fatal("deleted line still present")
	}
	for _, kept := range []string{"domain-needed", "unknown-directive=kept-verbatim", "# upstream", "  no-resolv"} {
		if !strings.Contains(out, kept) {
			t.Fatalf("delete lost unrelated line %q", kept)
		}
	}
	// stale guard
	err := c.DeleteLine(0, "not-the-raw")
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("want ConflictError for stale delete, got %v", err)
	}
}

func TestSetScalarUpdateDedupeRemove(t *testing.T) {
	c := loadSample(t)
	c.AddLine("cache-size", "512", false) // create a duplicate

	c.SetScalar("cache-size", "4096")
	if entries := c.Entries("cache-size"); len(entries) != 1 || entries[0].Value != "4096" {
		t.Fatalf("SetScalar must dedupe to one line with new value, got %+v", entries)
	}

	c.SetScalar("cache-size", "")
	if entries := c.Entries("cache-size"); len(entries) != 0 {
		t.Fatalf("empty value must remove option, got %+v", entries)
	}

	c.SetScalar("port", "5353")
	if got, ok := c.Scalar("port"); !ok || got != "5353" {
		t.Fatalf("SetScalar must create missing option, got %q ok=%v", got, ok)
	}
}

func TestSetFlagIdempotent(t *testing.T) {
	c := loadSample(t)
	c.SetFlag("dnssec", true)
	c.SetFlag("dnssec", true)
	if entries := c.Entries("dnssec"); len(entries) != 1 {
		t.Fatalf("SetFlag(true) twice must keep one line, got %d", len(entries))
	}
	c.SetFlag("dnssec", false)
	if c.HasFlag("dnssec") {
		t.Fatal("SetFlag(false) must remove the flag")
	}
	c.SetFlag("domain-needed", false)
	if strings.Contains(c.Serialize(), "domain-needed") {
		t.Fatal("SetFlag(false) must remove pre-existing flags too")
	}
}

func TestScalarTakesLastOccurrence(t *testing.T) {
	c := loadSample(t)
	c.AddLine("cache-size", "512", false)
	if got, _ := c.Scalar("cache-size"); got != "512" {
		t.Fatalf("dnsmasq takes the last value for repeated scalars, got %q", got)
	}
}

func TestRevChangesOnMutationAndIsStableOtherwise(t *testing.T) {
	c := loadSample(t)
	c2 := loadSample(t)
	if c.Rev != c2.Rev {
		t.Fatal("identical content must hash to the same rev")
	}
	before := c.Rev
	c.SetScalar("cache-size", "31337")
	if c.Rev == before {
		t.Fatal("rev must change after mutation")
	}
}

func TestIndicesStayContiguousAfterMutations(t *testing.T) {
	c := loadSample(t)
	c.AddLine("server", "8.8.8.8", false)
	_ = c.DeleteLine(0, "")
	c.SetScalar("cache-size", "1")
	for i, l := range c.Lines {
		if l.Idx != i {
			t.Fatalf("line %d has stale Idx %d after mutations", i, l.Idx)
		}
	}
}
