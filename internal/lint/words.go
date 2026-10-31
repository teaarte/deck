package lint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Words finds two phrases of deck's dictionary under root that one translation says alike, so a reader cannot tell
// them apart: a phrase and the name of its field (Kind, kind) are one, and sameWords lists the rest that stay.
func Words(root string) ([]Violation, error) {
	files, err := filepath.Glob(filepath.Join(root, "internal/words/*.json"))
	if err != nil {
		return nil, err
	}
	var out []Violation
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var ws map[string]string
		if err := json.Unmarshal(raw, &ws); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		used := map[int]bool{}
		by := map[string][]string{}
		for k, v := range ws {
			if !slices.ContainsFunc(by[v], func(o string) bool { return strings.EqualFold(o, k) }) {
				by[v] = append(by[v], k)
			}
		}
		rel, _ := filepath.Rel(root, f)
		for v, ks := range by {
			slices.Sort(ks)
			if len(ks) < 2 {
				continue
			}
			if i := slices.IndexFunc(sameWords, func(s []string) bool { return slices.Equal(s, ks) }); i >= 0 {
				used[i] = true
				continue
			}
			line := 1 + strings.Count(string(raw[:max(0, strings.Index(string(raw), "\""+jsonText(ks[1])+"\":"))]), "\n")
			out = append(out, Violation{filepath.ToSlash(rel), line, fmt.Sprintf("%q says %s alike: one phrase a meaning, or tell them apart", v, strings.Join(quoted(ks), " and "))})
		}
		if filepath.Base(f) == "ru.json" {
			for i, s := range sameWords {
				if !used[i] {
					out = append(out, Violation{filepath.ToSlash(rel), 1, fmt.Sprintf("sameWords lists %s, which say different things now: take it out", strings.Join(quoted(s), " and "))})
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b Violation) int { return a.Line - b.Line })
	return out, nil
}

var escaped = regexp.MustCompile(`^"|"$`)

func jsonText(s string) string {
	b, _ := json.Marshal(s)
	return escaped.ReplaceAllString(string(b), "")
}

func quoted(ks []string) []string {
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = fmt.Sprintf("%q", k)
	}
	return out
}
