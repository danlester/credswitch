package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

type FileKind int

const (
	KindConfig FileKind = iota
	KindCreds
)

// Section is a named block in an AWS config or credentials file.
// Lines is the raw content starting with the [header] line, with trailing
// blank lines trimmed. Round-tripping preserves indentation and comments
// inside the section verbatim.
type Section struct {
	Name  string
	Lines []string
}

func parseFile(path string, kind FileKind) ([]Section, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseBytes(data, kind), nil
}

func parseBytes(data []byte, kind FileKind) []Section {
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	var sections []Section
	var cur *Section
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if cur != nil {
				cur.Lines = trimTrailingBlank(cur.Lines)
				sections = append(sections, *cur)
			}
			cur = &Section{
				Name:  sectionNameFromHeader(trimmed, kind),
				Lines: []string{line},
			}
			continue
		}
		if cur != nil {
			cur.Lines = append(cur.Lines, line)
		}
		// Lines that appear before any [section] header are intentionally dropped —
		// they belong to no section and would have nowhere to go on rewrite.
	}
	if cur != nil {
		cur.Lines = trimTrailingBlank(cur.Lines)
		sections = append(sections, *cur)
	}
	return sections
}

func trimTrailingBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func sectionNameFromHeader(header string, kind FileKind) string {
	inner := strings.TrimSpace(header[1 : len(header)-1])
	if kind == KindConfig && strings.HasPrefix(inner, "profile ") {
		return strings.TrimSpace(inner[len("profile "):])
	}
	return inner
}

// normalizeSection returns a sorted list of "key=value" entries for a
// section, ignoring comments, blank lines, the [header] line, and surrounding
// whitespace. Two sections with the same logical content normalize to the
// same slice regardless of ordering or formatting differences.
func normalizeSection(s Section) []string {
	var out []string
	for _, line := range s.Lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			continue // header — name is compared separately
		}
		if idx := strings.Index(trimmed, "="); idx >= 0 {
			key := strings.TrimSpace(trimmed[:idx])
			val := strings.TrimSpace(trimmed[idx+1:])
			out = append(out, key+"="+val)
		} else {
			out = append(out, trimmed)
		}
	}
	sort.Strings(out)
	return out
}

func sectionsEqual(a, b Section) bool {
	return slices.Equal(normalizeSection(a), normalizeSection(b))
}

// writeSections writes sections back to disk, separated by a single blank
// line, atomically (temp file + rename).
func writeSections(path string, sections []Section) error {
	var b strings.Builder
	for i, s := range sections {
		if i > 0 {
			b.WriteString("\n")
		}
		for _, line := range s.Lines {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return atomicWrite(path, []byte(b.String()))
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".credswitch-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}
