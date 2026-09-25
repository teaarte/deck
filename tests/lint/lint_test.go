package lint

import (
	"strings"
	"testing"

	"deck2/internal/lint"
)

func TestRepositoryPassesChecks(t *testing.T) {
	vs, err := lint.Check("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		t.Error(v)
	}
}

func TestChecksCatchViolations(t *testing.T) {
	vs, err := lint.Check("testdata/bad")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range vs {
		got = append(got, v.String())
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"core.go:3: comment block of 3 lines",
		`core.go:8: requirement reference "FR-12"`,
		`core.go:11: vertical name "vacanc"`,
		`core.go:13: reasoning word "We"`,
		`core.go:19: write ".Q.FinishProcess" outside Store.Tx`,
		"web/app.ts:1: comment block of 3 lines",
		"web/index.html:3: comment block of 3 lines",
		`web/index.html:4: vertical name "vacanc"`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing violation %q in:\n%s", want, all)
		}
	}
	if len(vs) != 8 {
		t.Errorf("want exactly 8 violations, got %d:\n%s", len(vs), all)
	}
}
