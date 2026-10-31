package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// rule is a convention of the Go code: what it finds in a file and where it looks.
type rule struct {
	name string
	text string
	// in are the folders it looks in, out those of them it leaves.
	in, out []string
	find    func(f *ast.File, fset *token.FileSet) []int
}

var rules = []rule{
	{"now", "the wall clock outside the clock of deck: take Now of the config", []string{"internal/", "cmd/"}, nil, timeNow},
	{"go", "a bare go statement in a domain: run it through Go of the background", []string{"internal/app/"}, nil, goStmt},
	// fault names its types deck.<kind> and home its files deck.db, neither a command.
	{"deck", "a name of deck's command written out: take registry.Deck or registry.IsBuiltin", []string{"internal/", "cmd/"},
		[]string{"internal/registry/", "internal/fault/", "internal/home/", "internal/lint/"}, deckName},
}

// Rules finds what breaks the conventions of the Go code under root outside the exceptions, and the exceptions
// that break nothing any more.
func Rules(root string) ([]Violation, error) {
	found := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && slices.Contains(append(skipDirs, "desktop", "tests"), d.Name()) {
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
		if err != nil || isGenerated(string(src)) {
			return err
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return err
		}
		for _, r := range rules {
			under := func(p string) bool { return strings.HasPrefix(rel, p) }
			if !slices.ContainsFunc(r.in, under) || slices.ContainsFunc(r.out, under) {
				continue
			}
			for _, line := range r.find(f, fset) {
				found[r.name] = append(found[r.name], rel+":"+strconv.Itoa(line))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out []Violation
	for _, r := range rules {
		for _, at := range found[r.name] {
			if !slices.Contains(exceptions[r.name], at) {
				out = append(out, violationAt(at, r.text))
			}
		}
		for _, at := range exceptions[r.name] {
			if !slices.Contains(found[r.name], at) {
				out = append(out, violationAt(at, fmt.Sprintf("an exception of rule %q that breaks nothing: take it out of the list", r.name)))
			}
		}
	}
	return out, nil
}

func violationAt(at, text string) Violation {
	file, line, _ := strings.Cut(at, ":")
	n, _ := strconv.Atoi(line)
	return Violation{file, n, text}
}

// timeNow are the lines that read the wall clock: time.Now, time.Since and time.Until.
func timeNow(f *ast.File, fset *token.FileSet) []int {
	name := importName(f, "time")
	if name == "" {
		return nil
	}
	var out []int
	ast.Inspect(f, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok && slices.Contains([]string{"Now", "Since", "Until"}, s.Sel.Name) {
			if x, ok := s.X.(*ast.Ident); ok && x.Name == name && x.Obj == nil {
				out = append(out, fset.Position(s.Pos()).Line)
			}
		}
		return true
	})
	return out
}

// goStmt are the lines of go statements.
func goStmt(f *ast.File, fset *token.FileSet) []int {
	var out []int
	ast.Inspect(f, func(n ast.Node) bool {
		if g, ok := n.(*ast.GoStmt); ok {
			out = append(out, fset.Position(g.Pos()).Line)
		}
		return true
	})
	return out
}

// importName is the name a file imports a package by, "" when it does not.
func importName(f *ast.File, path string) string {
	for _, im := range f.Imports {
		if p, _ := strconv.Unquote(im.Path.Value); p == path {
			if im.Name != nil {
				return im.Name.Name
			}
			return filepath.Base(path)
		}
	}
	return ""
}

// deckName are the lines of string literals that begin with deck's prefix of commands.
func deckName(f *ast.File, fset *token.FileSet) []int {
	var out []int
	ast.Inspect(f, func(n ast.Node) bool {
		if l, ok := n.(*ast.BasicLit); ok && l.Kind == token.STRING {
			if v, err := strconv.Unquote(l.Value); err == nil && strings.HasPrefix(v, "deck.") {
				out = append(out, fset.Position(l.Pos()).Line)
			}
		}
		return true
	})
	return out
}

// refusalLit are the lines of composite literals of registry.Error.
func refusalLit(f *ast.File, fset *token.FileSet) []int {
	name := importName(f, "deck2/internal/registry")
	if name == "" {
		return nil
	}
	var out []int
	ast.Inspect(f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CompositeLit); ok {
			if s, ok := c.Type.(*ast.SelectorExpr); ok && s.Sel.Name == "Error" {
				if x, ok := s.X.(*ast.Ident); ok && x.Name == name {
					out = append(out, fset.Position(c.Pos()).Line)
				}
			}
		}
		return true
	})
	return out
}
