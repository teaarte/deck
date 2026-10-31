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
	codeExt = []string{".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".css", ".html", ".sql", ".py"}
	// lineMarks are the line comments of languages without //, and the lines with them that are no comment.
	lineMarks = map[string]struct {
		mark string
		not  *regexp.Regexp
	}{".sql": {"--", regexp.MustCompile(`^--\s*(\+goose|name:)`)}, ".py": {"#", regexp.MustCompile(`^#!`)}}
	// textDirs are where texts other than code are checked for vertical names: the schemas and the master.
	textDirs    = []string{"schemas/", "master/"}
	textExt     = []string{".json", ".md"}
	skipDirs    = []string{".git", ".claude", "node_modules", "dist", "testdata"}
	requirement = regexp.MustCompile(`\b(FR|NFR)-\d+|\bR\d{1,3}\b`)
	reasoning   = regexp.MustCompile(`(?i)\b(we|let's|i think|note that|basically|actually|obviously|for now|hack|in order to|probably|seems|i guess|turns out)\b`)
	verticals   = regexp.MustCompile(`(?i)vacanc|forecast|mailbox|brier|recruit|\bshorts\b|\bbtc\b`)
	// verticalFree are the built-in tools and the tests of the checks themselves; fixtures are in testdata.
	verticalFree = []string{"internal/tools/", "tests/lint/"}
	untyped      = regexp.MustCompile(`^\t\w+\s+interface\{\}`)
)

type Violation struct {
	File string
	Line int
	Text string
}

func (v Violation) String() string { return fmt.Sprintf("%s:%d: %s", v.File, v.Line, v.Text) }

// Check scans code under root for long comment blocks, requirement references and reasoning words in
// comments, vertical names outside built-in tools and the fixtures of tests, and sqlc parameters without a type.
func Check(root string) ([]Violation, error) {
	var out []Violation
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// docs/ holds texts for people, not code.
			if path != root && (slices.Contains(skipDirs, d.Name()) || filepath.ToSlash(path) == filepath.ToSlash(filepath.Join(root, "docs"))) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		text := slices.Contains(textExt, filepath.Ext(path)) && slices.ContainsFunc(textDirs, func(p string) bool { return strings.HasPrefix(rel, p) })
		if !slices.Contains(codeExt, filepath.Ext(path)) && !text {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if text {
			out = append(out, verticalNames(rel, string(src))...)
			return nil
		}
		if isGenerated(string(src)) {
			if strings.Contains(rel, "store/db/") {
				for i, line := range strings.Split(string(src), "\n") {
					if untyped.MatchString(line) {
						out = append(out, Violation{rel, i + 1, "a parameter of sqlc without a type: CAST it in its query, as one compared with 0 or '' turns NULL"})
					}
				}
			}
			return nil
		}
		out = append(out, checkComments(rel, string(src))...)
		if !slices.ContainsFunc(verticalFree, func(p string) bool { return strings.HasPrefix(rel, p) }) {
			out = append(out, verticalNames(rel, string(src))...)
		}
		return nil
	})
	return out, err
}

func verticalNames(file, src string) []Violation {
	var out []Violation
	for i, line := range strings.Split(src, "\n") {
		if m := verticals.FindString(line); m != "" {
			out = append(out, Violation{file, i + 1, fmt.Sprintf("vertical name %q in core code", m)})
		}
	}
	return out
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
	found := blocks(src)
	if m, ok := lineMarks[filepath.Ext(file)]; ok {
		found = lineBlocks(src, m.mark, m.not)
	}
	for _, c := range found {
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

// lineBlocks returns runs of whole-line comments of a language whose comments start with mark; a line that not
// matches is no comment.
func lineBlocks(src, mark string, not *regexp.Regexp) []comment {
	var out []comment
	last, header := -10, false
	for i, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		// The metadata of a Python script (PEP 723) is no comment.
		if t == "# /// script" || header && t == "# ///" {
			header = !header
			continue
		}
		if header || !strings.HasPrefix(t, mark) || not.MatchString(t) {
			continue
		}
		if last == i-1 && len(out) > 0 {
			out[len(out)-1].lines++
			out[len(out)-1].text += "\n" + t
		} else {
			out = append(out, comment{line: i + 1, lines: 1, text: t})
		}
		last = i
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
