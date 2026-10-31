package lint

import (
	"slices"
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
		"web/app.ts:1: comment block of 3 lines",
		"web/index.html:3: comment block of 3 lines",
		`web/index.html:4: vertical name "vacanc"`,
		"internal/store/db/q.sql.go:6: a parameter of sqlc without a type",
		"sql/q.sql:3: comment block of 3 lines",
		"script.py:8: comment block of 3 lines",
		`schemas/manifest.json:1: vertical name "forecast"`,
		`skills/notes/SKILL.md:5: vertical name "mailbox"`,
		`starters/demo/prompt.md:1: vertical name "Brier"`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing violation %q in:\n%s", want, all)
		}
	}
	if len(vs) != 13 {
		t.Errorf("want exactly 13 violations, got %d:\n%s", len(vs), all)
	}
}

func TestRepositoryKeepsTheRulesOfGo(t *testing.T) {
	vs, err := lint.Rules("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		t.Error(v)
	}
}

func TestRulesOfGoCatchViolations(t *testing.T) {
	vs, err := lint.Rules("testdata/rules")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range vs {
		got = append(got, v.String())
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"internal/app/x/x.go:8: the wall clock",
		"internal/app/x/x.go:9: a bare go statement",
		"internal/app/x/x.go:10: the wall clock",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing violation %q in:\n%s", want, all)
		}
	}
}

func TestRepositoryKeepsTheRulesOfTests(t *testing.T) {
	vs, err := lint.Tests("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		t.Error(v)
	}
}

func TestRulesOfTestsCatchViolations(t *testing.T) {
	vs, err := lint.Tests("testdata/tests")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range vs {
		got = append(got, v.String())
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"tests/scenarios/loose_test.go:1: a scenario outside the package of its area",
		"tests/scenarios/runs/stray_test.go:1: a file of scenarios is named runs_<topic>_test.go",
		"tests/scenarios/runs/runs_bad_test.go:4: the version of the manifest format as a literal",
		"tests/scenarios/runs/runs_bad_test.go:5: a token header by hand",
		"tests/scenarios/runs/runs_bad_test.go:6: a switch of real programs by hand",
		"tests/scenarios/runs/runs_bad_test.go:8: the base by hand",
		"tests/scenarios/runs/runs_bad_test.go:9: SQL in a test needs a comment above it",
		"tests/scenarios/runs/runs_bad_test.go:1: TestRealBrowserHere drives the real browser",
		"tests/scenarios/runs/runs_bad_test.go:13: SQL in a test needs a comment above it",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing violation %q in:\n%s", want, all)
		}
	}
	if len(vs) != 9 {
		t.Errorf("want exactly 9 violations, got %d:\n%s", len(vs), all)
	}
}

func TestDictionaryHasOnePhraseAMeaning(t *testing.T) {
	vs, err := lint.Words("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		t.Error(v)
	}
}

func TestDictionaryCheckCatchesTwoPhrasesSaidAlike(t *testing.T) {
	vs, err := lint.Words("testdata/bad")
	if err != nil {
		t.Fatal(err)
	}
	// The exceptions of deck's dictionary are not in the fixture.
	vs = slices.DeleteFunc(vs, func(v lint.Violation) bool { return strings.Contains(v.Text, "sameWords lists") })
	if len(vs) != 1 || !strings.Contains(vs[0].String(), `"Create module" and "Create project"`) {
		t.Fatalf("one violation of two phrases, not of a phrase and its field: %v", vs)
	}
}
