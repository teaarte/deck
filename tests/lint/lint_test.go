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
	for _, not := range []string{"internal/app/y/y.go:10:", "internal/app/y/y.go:11:", "q.sql:6:", "00099_x.sql:3:"} {
		if strings.Contains(all, not) {
			t.Errorf("a string that is no topic of an event is taken for one: %s in:\n%s", not, all)
		}
	}
	for _, want := range []string{
		"internal/app/x/x.go:8: the wall clock",
		"internal/app/x/x.go:9: a bare go statement",
		"internal/app/x/x.go:10: the wall clock",
		"internal/tools/t.go:6: a process started or a program looked up outside the sandbox",
		"internal/tools/t.go:14: a process started or a program looked up outside the sandbox",
		"internal/tools/t.go:9: an HTTP client or transport built outside internal/egress",
		"internal/tools/t.go:10: a file written by hand",
		"internal/tools/t.go:11: the environment of deck read outside cmd/deck",
		"internal/tools/t.go:13: an HTTP client or transport built outside internal/egress",
		"internal/app/y/y.go:6: a topic of events written out",
		"internal/app/y/y.go:7: a topic of events written out",
		"internal/app/y/y.go:8: a topic of events written out",
		"internal/app/y/y.go:9: a topic of events written out",
		"internal/store/queries/q.sql:2: 'now' outside the DEFAULT of a column",
		"internal/store/migrations/00099_x.sql:5: 'now' outside the DEFAULT of a column",
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

func TestRepositoryKeepsItsLayers(t *testing.T) {
	vs, err := lint.Layers("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		t.Error(v)
	}
}

func TestLayersCatchAnImportUp(t *testing.T) {
	vs, err := lint.Layers("testdata/layers")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range vs {
		got = append(got, v.String())
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"internal/store/store.go:6: internal/store (L1) imports internal/manifest (L2): an import goes only to a lower layer",
		"internal/stray/stray.go:1: package internal/stray is in no layer",
		"internal/app/a/a.go:4: internal/app/a imports the domain internal/app/b: only internal/app wires domains",
		"internal/api/api.go:3: internal/api imports the domain internal/app/tokens",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing violation %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "a.go:5") {
		t.Errorf("a domain imports a lower layer freely:\n%s", all)
	}
}

func TestRepositoryKeepsTablesToTheirList(t *testing.T) {
	vs, err := lint.Tables("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		t.Error(v)
	}
}

func TestTablesCatchTestsOffTheList(t *testing.T) {
	vs, err := lint.TablesOf("testdata/tables", map[string][]string{
		"internal/egress": {"Public", "Local", "listed", "named"},
		"internal/store":  {"Q"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range vs {
		got = append(got, v.String())
	}
	want := []string{
		"internal/egress/table_test.go:1: FuzzPublic tests no function of lint.Tabled for internal/egress",
		"internal/egress/table_test.go:1: TestNamed tests no function of lint.Tabled for internal/egress",
		"internal/egress/table_test.go:1: TestOther tests no function of lint.Tabled for internal/egress",
		"internal/egress/table_test.go:1: internal/egress.Local is in lint.Tabled without its table test TestLocal",
		"internal/egress/table_test.go:1: internal/egress.named is in lint.Tabled without its table test Test_named",
		"internal/home/within_test.go:1: a test outside tests/ is only the table test table_test.go of a function in lint.Tabled",
		"internal/store/table_test.go:1: a table test takes no harness: rows of input and answer only",
		"web/embed_test.go:1: a test outside tests/ is only the table test table_test.go of a function in lint.Tabled",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
