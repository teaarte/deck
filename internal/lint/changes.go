package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var changeDate = regexp.MustCompile(`^- (\d{4}-\d{2}-\d{2})\b`)

// Changes finds a line of master/changes.md under root dated before the line above it: new lines go at the end,
// so the master reads only those after the last one it remembers.
func Changes(root string) ([]Violation, error) {
	raw, err := os.ReadFile(filepath.Join(root, "master/changes.md"))
	if err != nil {
		return nil, err
	}
	var out []Violation
	last := ""
	for i, line := range strings.Split(string(raw), "\n") {
		m := changeDate.FindStringSubmatch(line)
		if m == nil {
			if strings.HasPrefix(line, "- ") {
				out = append(out, Violation{"master/changes.md", i + 1, "a line without its date: start it with - YYYY-MM-DD"})
			}
			continue
		}
		if m[1] < last {
			out = append(out, Violation{"master/changes.md", i + 1, fmt.Sprintf("%s comes after %s: a new line goes at the end with today's date", m[1], last)})
			continue
		}
		last = m[1]
	}
	return out, nil
}
