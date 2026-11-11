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

// Tabled are the pure functions on the trust boundary that keep a table test beside them, by package; a test
// outside tests/ is one of these and nothing else, and a function joins the list only here.
var Tabled = map[string][]string{
	"internal/egress": {"Public"},
}

const tableFile = "table_test.go"

var anyTest = regexp.MustCompile(`(?m)^func ((?:Test|Benchmark|Fuzz|Example)\w*)\(`)

// Tables finds tests outside tests/ that are not the table test of a listed function, and listed functions
// without one.
func Tables(root string) ([]Violation, error) {
	return TablesOf(root, Tabled)
}

// TablesOf is Tables by another list, for the check of the check.
func TablesOf(root string, tabled map[string][]string) ([]Violation, error) {
	var out []Violation
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "tests" || rel == "web/dist" || d.Name() == "node_modules" || d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".") && rel != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		pkg := filepath.ToSlash(filepath.Dir(rel))
		funcs, ok := tabled[pkg]
		if !ok || d.Name() != tableFile {
			out = append(out, Violation{rel, 1, "a test outside tests/ is only the table test " + tableFile + " of a function in lint.Tabled"})
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(b)
		if strings.Contains(src, "deck2/tests/") {
			out = append(out, Violation{rel, 1, "a table test takes no harness: rows of input and answer only"})
		}
		for _, f := range anyTest.FindAllStringSubmatch(src, -1) {
			name, ok := strings.CutPrefix(f[1], "Test")
			if !ok || !slices.Contains(funcs, name) {
				out = append(out, Violation{rel, 1, fmt.Sprintf("%s tests no function of lint.Tabled for %s", f[1], pkg)})
				continue
			}
			seen[pkg+"."+name] = true
		}
		return nil
	})
	for pkg, funcs := range tabled {
		for _, f := range funcs {
			if !seen[pkg+"."+f] {
				out = append(out, Violation{pkg + "/" + tableFile, 1, fmt.Sprintf("%s.%s is in lint.Tabled without its table test Test%s", pkg, f, f)})
			}
		}
	}
	slices.SortFunc(out, func(a, b Violation) int { return strings.Compare(a.String(), b.String()) })
	return out, err
}
