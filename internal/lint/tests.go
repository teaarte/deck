package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	formatVersion = regexp.MustCompile(`(?m)^const FormatVersion = (\d+)$`)
	realSwitch    = regexp.MustCompile(`os\.Getenv\("DECK_(REAL_\w+|SERVER)"\)`)
	rawBase       = regexp.MustCompile(`\.Store\.DB\.`)
	plant         = regexp.MustCompile(`\.(SQL|SQLRow|SQLRows|PlantRevision|Store\.Tx|Store\.Q\.(Set|Insert|Update|Delete|Create|Add|Put|Mark)\w*)\(`)
	testFunc      = regexp.MustCompile(`(?m)^func (Test\w+)\(`)
	scenarioFile  = regexp.MustCompile(`^(main|contract_[a-z]+|[a-z]+_[a-z0-9_]+)_test\.go$`)
)

// Tests finds what breaks the conventions of scenarios under root: files named by area and topic, the browser's
// tests in theirs, no manifest version, token header, real switch or base by hand, SQL only under a comment.
func Tests(root string) ([]Violation, error) {
	src, err := os.ReadFile(filepath.Join(root, "internal", "manifest", "types.go"))
	if err != nil {
		return nil, err
	}
	m := formatVersion.FindSubmatch(src)
	if m == nil {
		return nil, fmt.Errorf("no FormatVersion in internal/manifest/types.go")
	}
	version := regexp.MustCompile(`\\?"version\\?":\s*` + string(m[1]) + `\b`)
	var out []Violation
	scenarios := filepath.Join(root, "tests", "scenarios")
	err = filepath.WalkDir(scenarios, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		area := filepath.Base(filepath.Dir(path))
		name := filepath.Base(path)
		if filepath.Dir(path) == scenarios {
			out = append(out, Violation{rel, 1, "a scenario outside the package of its area"})
		} else if !scenarioFile.MatchString(name) || !(name == "main_test.go" || strings.HasPrefix(name, "contract_") || strings.HasPrefix(name, area+"_")) {
			out = append(out, Violation{rel, 1, fmt.Sprintf("a file of scenarios is named %s_<topic>_test.go, or contract_<provider>_test.go", area)})
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(b), "\n")
		for i, line := range lines {
			at := func(text string) { out = append(out, Violation{rel, i + 1, text}) }
			if version.MatchString(line) {
				at("the version of the manifest format as a literal: build the manifest with harness.Manifest")
			}
			if realSwitch.MatchString(line) {
				at("a switch of real programs by hand: harness.Needs")
			}
			if rawBase.MatchString(line) {
				at("the base by hand: e.SQL, e.SQLRow or e.PlantRevision with a comment of what it imitates")
			}
			if plant.MatchString(line) && !commentAbove(lines, i) {
				at("SQL in a test needs a comment above it: what old data it imitates or what deck shows nowhere")
			}
		}
		for _, line := range bearerLines(path, b) {
			out = append(out, Violation{rel, line, "a token header by hand: sign with e.Sign, harness.Bearer or e.As"})
		}
		if area != "browser" {
			for _, f := range testFunc.FindAllStringSubmatch(string(b), -1) {
				if strings.HasPrefix(f[1], "TestRealBrowser") {
					out = append(out, Violation{rel, 1, f[1] + " drives the real browser: it belongs in tests/scenarios/browser"})
				}
			}
		}
		return nil
	})
	return out, err
}

// commentAbove reports whether the statement at line i, or the call it is part of, has a comment line right above it.
func commentAbove(lines []string, i int) bool {
	for j := i; j > 0; j-- {
		above := strings.TrimSpace(lines[j-1])
		if strings.HasPrefix(above, "//") {
			return true
		}
		if !slices.ContainsFunc([]string{"(", "{", ","}, func(s string) bool { return strings.HasSuffix(above, s) }) {
			return false
		}
	}
	return false
}

// bearerLines are the lines of Go code that put a token after "Bearer ".
func bearerLines(path string, src []byte) []int {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return nil
	}
	var out []int
	ast.Inspect(f, func(n ast.Node) bool {
		if b, ok := n.(*ast.BinaryExpr); ok && b.Op == token.ADD {
			if lit, ok := b.X.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if v, err := strconv.Unquote(lit.Value); err == nil && strings.HasSuffix(v, "Bearer ") {
					out = append(out, fset.Position(b.Pos()).Line)
				}
			}
		}
		return true
	})
	return out
}
