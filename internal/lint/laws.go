package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Boundary is the code of the trust boundary: folders whole, files by name.
var Boundary = []string{"internal/access/", "internal/auth/", "internal/boxes/", "internal/egress/", "internal/gateway/",
	"internal/home/", "internal/registry/", "internal/sandbox/", "internal/api/who.go", "internal/manifest/catalog.go",
	"internal/manifest/rights.go"}

// quantifier is a word of a law, said of what holds in all cases: in a sentence, or beginning one; a name such as
// Every stays a name.
var quantifier = regexp.MustCompile(`\b(only|never|every)\b|(?:^|[.;:]\s+)(Only|Never|Every)\b`)

// Laws finds the doc comments of declarations on the trust boundary that say only, never or every without a table:
// such a comment is a law, and a law is the doc of a function of lint.Tabled, whose table test proves it.
func Laws(root string) ([]Violation, error) {
	return LawsOf(root, Boundary, Tabled)
}

// LawsOf is Laws by other lists, for the check of the check.
func LawsOf(root string, boundary []string, tabled map[string][]string) ([]Violation, error) {
	var out []Violation
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") || !slices.ContainsFunc(boundary, func(b string) bool {
			return rel == b || strings.HasSuffix(b, "/") && filepath.ToSlash(filepath.Dir(rel))+"/" == b
		}) {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		laws := tabled[filepath.ToSlash(filepath.Dir(rel))]
		check := func(doc *ast.CommentGroup, fn string) {
			if doc == nil || fn != "" && slices.Contains(laws, fn) {
				return
			}
			if m := quantifier.FindStringSubmatch(doc.Text()); m != nil {
				w := m[1] + m[2]
				what := "this comment"
				if fn != "" {
					what = fn
				}
				out = append(out, Violation{rel, fset.Position(doc.Pos()).Line, fmt.Sprintf("%q is a law on the trust boundary: put %s in lint.Tabled with its table test, or say it without the quantifier", strings.ToLower(w), what)})
			}
		}
		check(f.Doc, "")
		for _, decl := range f.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				check(decl.Doc, decl.Name.Name)
			case *ast.GenDecl:
				check(decl.Doc, "")
				for _, s := range decl.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						check(s.Doc, "")
						// Fields and methods, of inner structs too, by their doc and their line comment.
						ast.Inspect(s.Type, func(n ast.Node) bool {
							if fd, ok := n.(*ast.Field); ok {
								check(fd.Doc, "")
								check(fd.Comment, "")
							}
							return true
						})
					case *ast.ValueSpec:
						check(s.Doc, "")
					}
				}
			}
		}
		return nil
	})
	slices.SortFunc(out, func(a, b Violation) int { return strings.Compare(a.String(), b.String()) })
	return out, err
}
