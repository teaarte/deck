package lint

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// module is the path of deck's Go module.
const module = "deck2/"

// layers are deck's packages from the base up; a package imports only those of a layer below its own, and a domain,
// a package of internal/app/, only internal/app imports.
var layers = [][]string{
	{".", "internal/fault", "internal/nonce", "internal/untrusted", "internal/events", "internal/release", "internal/home", "internal/words",
		"internal/store/db", "internal/lint", "internal/provider/codex/gen", "master", "schemas", "skills", "starters", "web"},
	{"internal/store", "internal/registry", "internal/theme", "internal/egress"},
	{"internal/manifest", "internal/health", "internal/ask", "internal/files", "internal/sandbox", "internal/auth"},
	{"internal/docs", "internal/boxes", "internal/script", "internal/trigger", "internal/provider", "internal/mcphost"},
	{"internal/gateway", "internal/models", "internal/provider/acp", "internal/provider/claudecode", "internal/provider/codex",
		"internal/screen", "internal/access"},
	{"internal/process", "internal/tools"},
	{domains + "*"},
	{"internal/app"},
	{"internal/api"},
	{"cmd/deck", "cmd/deck-release"},
}

// domains is the folder of the domains.
const domains = "internal/app/"

// layerOf is the layer of the package in folder pkg, -1 when the table has none.
func layerOf(pkg string) int {
	for i, l := range layers {
		if slices.Contains(l, pkg) || slices.Contains(l, domains+"*") && domain(pkg) {
			return i
		}
	}
	return -1
}

func domain(pkg string) bool {
	rest, ok := strings.CutPrefix(pkg, domains)
	return ok && rest != "" && !strings.Contains(rest, "/")
}

// Layers finds the imports of deck's Go code under root that go up or across the layers, and the packages in no
// layer, outside the exceptions of "layers", and the exceptions that break nothing any more.
func Layers(root string) ([]Violation, error) {
	var found []Violation
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && slices.Contains(append(skipDirs, "desktop", "tests", "docs"), d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if filepath.Ext(path) != ".go" || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, src, parser.ImportsOnly)
		if err != nil {
			return err
		}
		pkg := filepath.ToSlash(filepath.Dir(rel))
		own := layerOf(pkg)
		if own < 0 {
			found = append(found, Violation{rel, fset.Position(f.Package).Line, fmt.Sprintf("package %s is in no layer of internal/lint/layers.go", pkg)})
			return nil
		}
		for _, im := range f.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			to, ok := strings.CutPrefix(p, module)
			if !ok || to == pkg {
				continue
			}
			line := fset.Position(im.Pos()).Line
			switch l := layerOf(to); {
			case domain(to) && pkg != "internal/app":
				found = append(found, Violation{rel, line, fmt.Sprintf("%s imports the domain %s: only internal/app wires domains", pkg, to)})
			case l >= own:
				found = append(found, Violation{rel, line, fmt.Sprintf("%s (L%d) imports %s (L%d): an import goes only to a lower layer", pkg, own, to, l)})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out []Violation
	at := func(v Violation) string { return v.File + ":" + strconv.Itoa(v.Line) }
	for _, v := range found {
		if !slices.Contains(exceptions["layers"], at(v)) {
			out = append(out, v)
		}
	}
	for _, e := range exceptions["layers"] {
		if !slices.ContainsFunc(found, func(v Violation) bool { return at(v) == e }) {
			out = append(out, violationAt(e, `an exception of rule "layers" that breaks nothing: take it out of the list`))
		}
	}
	return out, nil
}
