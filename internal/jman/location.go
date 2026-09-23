package jman

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
)

func ParseLocation(s string) (string, int, int, error) {
	parts := strings.Split(s, ":")
	n := len(parts)
	if n < 2 {
		return s, 0, 0, nil
	}
	last, e := strconv.Atoi(parts[n-1])
	if e != nil {
		return s, 0, 0, nil
	}
	if n >= 3 {
		if line, e := strconv.Atoi(parts[n-2]); e == nil {
			return strings.Join(parts[:n-2], ":"), line, last, nil
		}
	}
	return strings.Join(parts[:n-1], ":"), last, 0, nil
}
func FileURI(path string) string { return (&url.URL{Scheme: "file", Path: path}).String() }
func URIPath(s string) string {
	u, e := url.Parse(s)
	if e != nil || u.Scheme != "file" {
		return ""
	}
	return u.Path
}
func Canonical(path string) (string, error) {
	p, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	return filepath.EvalSymlinks(p)
}
func FindProject(path string) (string, error) {
	info, e := os.Stat(path)
	if e != nil {
		return "", e
	}
	if !info.IsDir() {
		path = filepath.Dir(path)
	}
	path, e = Canonical(path)
	if e != nil {
		return "", e
	}
	nearest := ""
	for p := path; ; p = filepath.Dir(p) {
		for _, name := range []string{"settings.gradle", "settings.gradle.kts"} {
			if _, e := os.Stat(filepath.Join(p, name)); e == nil {
				return p, nil
			}
		}
		if nearest == "" {
			for _, name := range []string{"build.gradle", "build.gradle.kts", "pom.xml", ".project"} {
				if _, e := os.Stat(filepath.Join(p, name)); e == nil {
					nearest = p
					break
				}
			}
		}
		if _, e := os.Stat(filepath.Join(p, ".git")); e == nil {
			break
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if nearest != "" {
		return nearest, nil
	}
	return "", fmt.Errorf("no build root found for %s; use --project PATH", path)
}
func identifier(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_' || r == '$'
}

// Position converts a 1-based public code-point column into a 0-based UTF-16 LSP position.
func Position(text string, line, column int, symbol string, occurrence int) (int, error) {
	lines := strings.Split(text, "\n")
	if line < 1 || line > len(lines) {
		return 0, fmt.Errorf("line %d outside file (1..%d)", line, len(lines))
	}
	runes := []rune(strings.TrimSuffix(lines[line-1], "\r"))
	if symbol != "" {
		want := []rune(symbol)
		var matches []int
		for i := 0; i+len(want) <= len(runes); i++ {
			if string(runes[i:i+len(want)]) == symbol && (i == 0 || !identifier(runes[i-1])) && (i+len(want) == len(runes) || !identifier(runes[i+len(want)])) {
				matches = append(matches, i+1)
			}
		}
		if len(matches) == 0 {
			return 0, fmt.Errorf("identifier %q is absent on line %d", symbol, line)
		}
		if column > 0 {
			selected := 0
			for i, start := range matches {
				if column >= start && column < start+len(want) {
					selected = i + 1
					break
				}
			}
			if selected == 0 {
				return 0, fmt.Errorf("column %d does not select %q; candidate columns: %v", column, symbol, matches)
			}
			if occurrence > 0 && occurrence != selected {
				return 0, fmt.Errorf("column and occurrence select different tokens")
			}
			occurrence = selected
		}
		if occurrence == 0 && len(matches) > 1 {
			return 0, fmt.Errorf("ambiguous identifier %q: columns %v; use --occurrence N or --column", symbol, matches)
		}
		if occurrence == 0 {
			occurrence = 1
		}
		if occurrence < 1 || occurrence > len(matches) {
			return 0, fmt.Errorf("occurrence outside 1..%d", len(matches))
		}
		column = matches[occurrence-1]
	}
	if column < 1 || column > len(runes)+1 {
		return 0, fmt.Errorf("provide --symbol or column in 1..%d", len(runes)+1)
	}
	return len(utf16.Encode(runes[:column-1])), nil
}
