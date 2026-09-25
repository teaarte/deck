package lint

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var (
	codeExt      = []string{".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".css", ".html"}
	skipDirs     = []string{".git", "node_modules", "dist", "testdata"}
	requirement  = regexp.MustCompile(`\b(FR|NFR)-\d+|\bR\d{1,3}\b`)
	reasoning    = regexp.MustCompile(`(?i)\b(we|let's|i think|note that|basically|actually|obviously|for now|hack|in order to|probably|seems|i guess|turns out)\b`)
	verticals    = regexp.MustCompile(`(?i)vacanc|forecast|mailbox|brier|recruit`)
	verticalFree = []string{"tests/", "examples/", "internal/tools/"}
)

type Violation struct {
	File string
	Line int
	Text string
}

func (v Violation) String() string { return fmt.Sprintf("%s:%d: %s", v.File, v.Line, v.Text) }

// Check scans code under root for long comment blocks, requirement references and reasoning words in
// comments, and for vertical names outside built-in tools and test fixtures.
func Check(root string) ([]Violation, error) {
	var out []Violation
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && slices.Contains(skipDirs, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !slices.Contains(codeExt, filepath.Ext(path)) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if isGenerated(string(src)) {
			return nil
		}
		out = append(out, checkComments(rel, string(src))...)
		if !strings.HasSuffix(rel, "_test.go") && !slices.ContainsFunc(verticalFree, func(p string) bool { return strings.HasPrefix(rel, p) }) {
			for i, line := range strings.Split(string(src), "\n") {
				if m := verticals.FindString(line); m != "" {
					out = append(out, Violation{rel, i + 1, fmt.Sprintf("vertical name %q in core code", m)})
				}
			}
		}
		return nil
	})
	return out, err
}

func isGenerated(src string) bool {
	head, _, _ := strings.Cut(src, "\npackage ")
	return strings.Contains(head, "Code generated") && strings.Contains(head, "DO NOT EDIT")
}

type comment struct {
	line, lines int
	text        string
}

func checkComments(file, src string) []Violation {
	var out []Violation
	for _, c := range blocks(src) {
		if c.lines > 2 {
			out = append(out, Violation{file, c.line, fmt.Sprintf("comment block of %d lines; keep it to 2", c.lines)})
		}
		if m := requirement.FindString(c.text); m != "" {
			out = append(out, Violation{file, c.line, fmt.Sprintf("requirement reference %q in a comment", m)})
		}
		if m := reasoning.FindString(c.text); m != "" {
			out = append(out, Violation{file, c.line, fmt.Sprintf("reasoning word %q in a comment", m)})
		}
	}
	return out
}

// blocks returns comment blocks: runs of line comments on adjacent lines and /* */ comments.
func blocks(src string) []comment {
	var out []comment
	line, lastLine := 1, -10
	var quote byte
	for i := 0; i < len(src); i++ {
		ch := src[i]
		if ch == '\n' {
			line++
		}
		if quote != 0 {
			switch {
			case ch == '\\' && quote != '`':
				i++
			case ch == quote:
				quote = 0
			case ch == '\n' && quote != '`':
				quote = 0
			}
			continue
		}
		switch {
		case ch == '"' || ch == '\'' || ch == '`':
			quote = ch
		case strings.HasPrefix(src[i:], "//"):
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = len(src) - i
			}
			text := src[i+2 : i+end]
			i += end - 1
			if strings.HasPrefix(text, "go:") || strings.HasPrefix(text, "nolint") {
				continue
			}
			if lastLine == line-1 && len(out) > 0 {
				out[len(out)-1].lines++
				out[len(out)-1].text += "\n" + text
			} else {
				out = append(out, comment{line: line, lines: 1, text: text})
			}
			lastLine = line
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i:], "*/")
			if end < 0 {
				end = len(src) - i
			}
			text := src[i+2 : i+end]
			n := strings.Count(text, "\n")
			out = append(out, comment{line: line, lines: n + 1, text: text})
			line += n
			i += end + 1
		}
	}
	return out
}
