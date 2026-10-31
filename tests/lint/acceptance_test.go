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
// later step, and, where docs/spec.md is on the disk, that the rows are the acceptance items of the spec.
func TestAcceptanceIsTraced(t *testing.T) {
	trace, err := os.ReadFile("../../docs/acceptance.md")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	var items []string
	for _, l := range strings.Split(string(trace), "\n") {
		cells := strings.Split(strings.Trim(l, "| "), " | ")
		if !strings.HasPrefix(l, "| ") || len(cells) != 2 || cells[0] == "Пункт" {
			continue
		}
		rows[cells[0]] = cells[1]
		items = append(items, cells[0])
	}
	if len(items) == 0 {
		t.Fatal("docs/acceptance.md has no rows")
	}
	spec, err := os.ReadFile("../../docs/spec.md")
	switch {
	case errors.Is(err, fs.ErrNotExist):
		t.Log("docs/spec.md lives only on the owner's disk: the rows are not checked against its items")
	case err != nil:
		t.Fatal(err)
	default:
		items = specItems(t, string(spec))
	}
	m := regexp.MustCompile(`Сделаны шаги: 1–(\d+)\.`).FindStringSubmatch(string(trace))
	if m == nil {
		t.Fatal("docs/acceptance.md must say which steps are done")
	}
	done, _ := strconv.Atoi(m[1])
	tests := testNames(t)
	for _, item := range items {
		cov, ok := rows[item]
		if !ok {
			t.Errorf("acceptance item has no row: %s", item)
			continue
		}
		delete(rows, item)
		names := regexp.MustCompile(`\bTest[A-Za-z0-9_]+`).FindAllString(cov, -1)
		for _, n := range names {
			if !tests[n] {
				t.Errorf("%q names a test that does not exist: %s", item, n)
			}
		}
		later := regexp.MustCompile(`^(шаг|этап) (\d+)`).FindStringSubmatch(cov)
		switch {
		case strings.HasPrefix(cov, "ручная: "):
		case later != nil:
			if n, _ := strconv.Atoi(later[2]); later[1] == "шаг" && n <= done {
				t.Errorf("%q waits for step %d, which is done", item, n)
			}
		case len(names) == 0:
			t.Errorf("acceptance item has no coverage: %s", item)
		}
	}
	for item := range rows {
		t.Errorf("row for an item the spec does not have: %s", item)
	}
}

// specItems are the items of the section of acceptance of the spec.
func specItems(t *testing.T, spec string) []string {
	_, sec, _ := strings.Cut(spec, "\n## Приёмка\n")
	sec, _, _ = strings.Cut(sec, "\n## ")
	var items []string
	for _, l := range strings.Split(sec, "\n") {
		if item, ok := strings.CutPrefix(l, "- [ ] "); ok {
			items = append(items, strings.TrimSpace(item))
		}
	}
	if len(items) == 0 {
		t.Fatal("no acceptance items found in the spec")
	}
	return items
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
