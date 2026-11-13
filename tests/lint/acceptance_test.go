package lint

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestAcceptanceIsTraced checks that every row of docs/acceptance.md names tests that exist, a manual check or a
// later step, that the rows are as many as the items it states the spec has, and that count against docs/spec.md on disk.
func TestAcceptanceIsTraced(t *testing.T) {
	trace, err := os.ReadFile("../../docs/acceptance.md")
	if err != nil {
		t.Fatal(err)
	}
	var items, covs []string
	for _, l := range strings.Split(string(trace), "\n") {
		cells := strings.Split(strings.Trim(l, "| "), " | ")
		if !strings.HasPrefix(l, "| ") || len(cells) != 2 || cells[0] == "Item" {
			continue
		}
		items = append(items, cells[0])
		covs = append(covs, cells[1])
	}
	if len(items) == 0 {
		t.Fatal("docs/acceptance.md has no rows")
	}
	stated := regexp.MustCompile(`(?m)^The spec has (\d+) acceptance items\.$`).FindStringSubmatch(string(trace))
	if stated == nil {
		t.Fatal("docs/acceptance.md must state how many acceptance items the spec has")
	}
	count, _ := strconv.Atoi(stated[1])
	if count != len(items) {
		t.Errorf("docs/acceptance.md states %d spec items and has %d rows: the n-th row stands for the n-th item", count, len(items))
	}
	spec, err := os.ReadFile("../../docs/spec.md")
	switch {
	case errors.Is(err, fs.ErrNotExist):
		t.Log("docs/spec.md lives only on the owner's disk: the stated count is not checked against it")
	case err != nil:
		t.Fatal(err)
	default:
		if n := specItems(t, string(spec)); n != count {
			t.Errorf("the spec has %d acceptance items, docs/acceptance.md states %d", n, count)
		}
	}
	m := regexp.MustCompile(`Steps done: 1–(\d+)\.`).FindStringSubmatch(string(trace))
	if m == nil {
		t.Fatal("docs/acceptance.md must say which steps are done")
	}
	done, _ := strconv.Atoi(m[1])
	tests := testNames(t)
	for i, item := range items {
		cov := covs[i]
		names := regexp.MustCompile(`\bTest[A-Za-z0-9_]+`).FindAllString(cov, -1)
		for _, n := range names {
			if !tests[n] {
				t.Errorf("%q names a test that does not exist: %s", item, n)
			}
		}
		later := regexp.MustCompile(`^(step|stage) (\d+)`).FindStringSubmatch(cov)
		switch {
		case strings.HasPrefix(cov, "manual: "):
		case later != nil:
			if n, _ := strconv.Atoi(later[2]); later[1] == "step" && n <= done {
				t.Errorf("%q waits for step %d, which is done", item, n)
			}
		case len(names) == 0:
			t.Errorf("acceptance item has no coverage: %s", item)
		}
	}
}

// specItems counts the acceptance items of the spec: its one list of `- [ ] ` lines, all under one `## ` section.
func specItems(t *testing.T, spec string) int {
	n, section, sections := 0, 0, map[int]bool{}
	for _, l := range strings.Split(spec, "\n") {
		if strings.HasPrefix(l, "## ") {
			section++
		}
		if strings.HasPrefix(l, "- [ ] ") {
			n++
			sections[section] = true
		}
	}
	if n == 0 {
		t.Fatal("no acceptance items found in the spec")
	}
	if len(sections) != 1 {
		t.Fatalf("the spec has `- [ ] ` items in %d sections, acceptance must be the one list of them", len(sections))
	}
	return n
}

func testNames(t *testing.T) map[string]bool {
	names := map[string]bool{}
	re := regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\)`)
	err := filepath.WalkDir("..", func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, err := os.ReadFile(p)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			names[m[1]] = true
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}
