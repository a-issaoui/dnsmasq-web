package dnsmasq

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// Line is a single physical line of dnsmasq.conf. Every line of the file is
// kept — directives, comments and blanks — so a round-trip through the model
// never loses or reorders anything.
type Line struct {
	Idx   int    `json:"idx"`
	Raw   string `json:"raw"`
	Key   string `json:"key"`   // directive name; "" for comments/blanks
	Value string `json:"value"` // text after '='; "" for flag directives
	Flag  bool   `json:"flag"`  // directive present without '=' (e.g. "dnssec")
}

// ConfFile is the parsed configuration: an ordered list of lines.
type ConfFile struct {
	Path  string `json:"path"`
	Lines []Line `json:"lines"`
	Rev   string `json:"rev"` // content hash, used for optimistic concurrency
}

// LoadConf reads and parses a dnsmasq configuration file. A missing file
// yields an empty (but writable) ConfFile.
func LoadConf(path string) (*ConfFile, error) {
	c := &ConfFile{Path: path, Lines: []Line{}}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			c.Rev = c.hash()
			return c, nil
		}
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	idx := 0
	for sc.Scan() {
		c.Lines = append(c.Lines, parseLine(idx, sc.Text()))
		idx++
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	c.Rev = c.hash()
	return c, nil
}

func parseLine(idx int, raw string) Line {
	l := Line{Idx: idx, Raw: raw}
	t := strings.TrimSpace(raw)
	if t == "" || strings.HasPrefix(t, "#") {
		return l
	}
	if eq := strings.Index(t, "="); eq >= 0 {
		l.Key = strings.TrimSpace(t[:eq])
		l.Value = strings.TrimSpace(t[eq+1:])
	} else {
		l.Key = t
		l.Flag = true
	}
	return l
}

// Serialize renders the file back to text, byte-identical for untouched lines.
func (c *ConfFile) Serialize() string {
	var sb strings.Builder
	for _, l := range c.Lines {
		sb.WriteString(l.Raw)
		sb.WriteString("\n")
	}
	return sb.String()
}

func (c *ConfFile) hash() string {
	h := sha256.Sum256([]byte(c.Serialize()))
	return hex.EncodeToString(h[:8])
}

func renderLine(key, value string, flag bool) string {
	if flag {
		return key
	}
	return key + "=" + value
}

func (c *ConfFile) reindex() {
	for i := range c.Lines {
		c.Lines[i].Idx = i
	}
	c.Rev = c.hash()
}

// Entries returns all directive lines with the given key.
func (c *ConfFile) Entries(key string) []Line {
	var out []Line
	for _, l := range c.Lines {
		if l.Key == key {
			out = append(out, l)
		}
	}
	return out
}

// Scalar returns the value of the last occurrence of key ("" if absent).
// dnsmasq itself takes the last value for repeated scalar options.
func (c *ConfFile) Scalar(key string) (string, bool) {
	for i := len(c.Lines) - 1; i >= 0; i-- {
		if c.Lines[i].Key == key {
			return c.Lines[i].Value, true
		}
	}
	return "", false
}

// HasFlag reports whether a flag directive is present.
func (c *ConfFile) HasFlag(key string) bool {
	_, ok := c.Scalar(key)
	return ok
}

// AddLine appends a new directive. It is inserted after the last existing
// line with the same key to keep related directives grouped; otherwise it is
// appended at the end of the file.
func (c *ConfFile) AddLine(key, value string, flag bool) Line {
	nl := parseLine(0, renderLine(key, value, flag))
	pos := -1
	for i, l := range c.Lines {
		if l.Key == key {
			pos = i
		}
	}
	insertAt := len(c.Lines)
	if pos >= 0 {
		insertAt = pos + 1
	}
	c.Lines = append(c.Lines, Line{})
	copy(c.Lines[insertAt+1:], c.Lines[insertAt:])
	c.Lines[insertAt] = nl
	c.reindex()
	return c.Lines[insertAt]
}

// UpdateLine replaces the line at idx. expectRaw guards against concurrent
// edits: if non-empty and it no longer matches, the update is refused.
func (c *ConfFile) UpdateLine(idx int, expectRaw, key, value string, flag bool) error {
	if idx < 0 || idx >= len(c.Lines) {
		return fmt.Errorf("line %d does not exist", idx)
	}
	if expectRaw != "" && c.Lines[idx].Raw != expectRaw {
		return &ConflictError{fmt.Sprintf("line %d changed since it was loaded (expected %q, found %q)", idx, expectRaw, c.Lines[idx].Raw)}
	}
	c.Lines[idx] = parseLine(idx, renderLine(key, value, flag))
	c.reindex()
	return nil
}

// UpdateRawLine replaces the raw text of the line at idx (used by the config
// explorer for comment/blank/arbitrary edits).
func (c *ConfFile) UpdateRawLine(idx int, expectRaw, raw string) error {
	if idx < 0 || idx >= len(c.Lines) {
		return fmt.Errorf("line %d does not exist", idx)
	}
	if expectRaw != "" && c.Lines[idx].Raw != expectRaw {
		return &ConflictError{fmt.Sprintf("line %d changed since it was loaded", idx)}
	}
	if strings.ContainsAny(raw, "\n\r") {
		return fmt.Errorf("line must not contain newlines")
	}
	c.Lines[idx] = parseLine(idx, raw)
	c.reindex()
	return nil
}

// DeleteLine removes the line at idx with the same concurrency guard.
func (c *ConfFile) DeleteLine(idx int, expectRaw string) error {
	if idx < 0 || idx >= len(c.Lines) {
		return fmt.Errorf("line %d does not exist", idx)
	}
	if expectRaw != "" && c.Lines[idx].Raw != expectRaw {
		return &ConflictError{fmt.Sprintf("line %d changed since it was loaded", idx)}
	}
	c.Lines = append(c.Lines[:idx], c.Lines[idx+1:]...)
	c.reindex()
	return nil
}

// SetScalar sets a single-valued option: the first occurrence is updated,
// any duplicates are removed, and an empty value removes the option entirely.
func (c *ConfFile) SetScalar(key, value string) {
	if strings.TrimSpace(value) == "" {
		c.removeAll(key)
		return
	}
	first := -1
	kept := c.Lines[:0]
	for _, l := range c.Lines {
		if l.Key == key {
			if first == -1 {
				first = len(kept)
				kept = append(kept, l)
			}
			continue
		}
		kept = append(kept, l)
	}
	c.Lines = kept
	nl := parseLine(0, renderLine(key, value, false))
	if first >= 0 {
		c.Lines[first] = nl
	} else {
		c.Lines = append(c.Lines, nl)
	}
	c.reindex()
}

// SetFlag adds or removes a flag directive.
func (c *ConfFile) SetFlag(key string, on bool) {
	if !on {
		c.removeAll(key)
		return
	}
	if c.HasFlag(key) {
		return
	}
	c.Lines = append(c.Lines, parseLine(0, key))
	c.reindex()
}

func (c *ConfFile) removeAll(key string) {
	kept := c.Lines[:0]
	for _, l := range c.Lines {
		if l.Key != key {
			kept = append(kept, l)
		}
	}
	c.Lines = kept
	c.reindex()
}

// ConflictError marks optimistic-concurrency failures so the API layer can
// answer 409 instead of 400.
type ConflictError struct{ msg string }

func (e *ConflictError) Error() string { return e.msg }
