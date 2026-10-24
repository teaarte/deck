package lint

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	// themeFiles are the files of the client that hold the theme's own values and the palette.
	themeFiles = []string{"web/src/theme/", "schemas/palette.json"}
	hexColor   = regexp.MustCompile(`#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3,4})\b`)
	fnColor    = regexp.MustCompile(`\b(?:rgba?|hsla?)\(`)
	cssRadius  = regexp.MustCompile(`border(?:-[a-z]+)*-radius\s*:\s*([^;]+)`)
	jsxRadius  = regexp.MustCompile(`\bradius=`)
	jsRadius   = regexp.MustCompile(`borderRadius\s*:\s*['"]?\d`)
	digit      = regexp.MustCompile(`\d`)
)

// Unthemed finds in the client what a theme cannot change: colors, numbers of a border-radius and radius="…" of
// Mantine in *.module.css and *.tsx outside the files of the theme and the palette.
func Unthemed(root string) ([]Violation, error) {
	var out []Violation
	src := filepath.Join(root, "web", "src")
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		css, tsx := strings.HasSuffix(rel, ".module.css"), strings.HasSuffix(rel, ".tsx")
		if (!css && !tsx) || slices.ContainsFunc(themeFiles, func(p string) bool { return strings.HasPrefix(rel, p) }) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if hexColor.MatchString(line) || fnColor.MatchString(line) {
				out = append(out, Violation{rel, i + 1, "a color of its own, not of the theme"})
			}
			if m := cssRadius.FindStringSubmatch(line); css && m != nil && digit.MatchString(m[1]) && !strings.Contains(m[1], "var(") && strings.TrimSpace(m[1]) != "0" {
				out = append(out, Violation{rel, i + 1, "a border-radius of its own, not of the theme"})
			}
			if tsx && (jsxRadius.MatchString(line) || jsRadius.MatchString(line)) {
				out = append(out, Violation{rel, i + 1, "a radius of its own, not of the theme"})
			}
		}
		return nil
	})
	return out, err
}

// Ratchet is the list of files that still have unthemed values: the count of each and why, which only goes down.
type Ratchet map[string]int

// ReadRatchet reads lines of "<count> <file> <reason>"; lines starting with # are comments.
func ReadRatchet(path string) (Ratchet, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := Ratchet{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		count, err := strconv.Atoi(parts[0])
		if len(parts) < 3 || err != nil {
			return nil, fmt.Errorf("%s:%d: want <count> <file> <reason>", path, n)
		}
		out[parts[1]] = count
	}
	return out, sc.Err()
}

// Against says where the violations differ from the ratchet: a file not in it, more than it allows, or fewer, which
// lowers its count.
func (r Ratchet) Against(vs []Violation) []string {
	by := map[string]int{}
	for _, v := range vs {
		by[v.File]++
	}
	var out []string
	for f, n := range by {
		switch allowed, ok := r[f]; {
		case !ok:
			out = append(out, fmt.Sprintf("%s: %d unthemed values; take them from the theme", f, n))
		case n > allowed:
			out = append(out, fmt.Sprintf("%s: %d unthemed values, the ratchet allows %d", f, n, allowed))
		case n < allowed:
			out = append(out, fmt.Sprintf("%s: %d unthemed values left, lower the ratchet from %d", f, n, allowed))
		}
	}
	for f := range r {
		if by[f] == 0 {
			out = append(out, fmt.Sprintf("%s: clean, take it off the ratchet", f))
		}
	}
	slices.Sort(out)
	return out
}
