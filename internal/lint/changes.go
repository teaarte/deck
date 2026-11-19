package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var changeDate = regexp.MustCompile(`^- (\d{4}-\d{2}-\d{2})\b`)

// modulesSince is the first day whose lines of master/changes.md end with what a module does about them.
const modulesSince = "2026-11-10"

// Changes finds a line of master/changes.md under root dated before the line above it, so the master misses it, and
// one since modulesSince that does not end with what a module does about it.
func Changes(root string) ([]Violation, error) {
	raw, err := os.ReadFile(filepath.Join(root, "master/changes.md"))
	if err != nil {
		return nil, err
	}
	var out []Violation
	last, open, said := "", 0, false
	closeEntry := func() {
		if open > 0 && !said {
			out = append(out, Violation{"master/changes.md", open, "a line since " + modulesSince + " ends with what a module does: Modules: <what to look for and what to put instead>, or Modules: nothing."})
		}
		open = 0
	}
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "  ") {
			said = said || strings.HasPrefix(line, "  Modules: ")
			continue
		}
		closeEntry()
		m := changeDate.FindStringSubmatch(line)
		if m == nil {
			if strings.HasPrefix(line, "- ") {
				out = append(out, Violation{"master/changes.md", i + 1, "a line without its date: start it with - YYYY-MM-DD"})
			}
			continue
		}
		if m[1] >= modulesSince {
			open, said = i+1, false
		}
		if m[1] < last {
			out = append(out, Violation{"master/changes.md", i + 1, fmt.Sprintf("%s comes after %s: a new line goes at the end with today's date", m[1], last)})
			continue
		}
		last = m[1]
	}
	closeEntry()
	return out, nil
}
