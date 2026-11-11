package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
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
	// fault names its types deck.<kind> and home its files deck.db, neither a command; deck.localhost is a host.
	{"deck", "a name of deck's command written out: take registry.Deck or registry.IsBuiltin", []string{"internal/", "cmd/"},
		[]string{"internal/registry/", "internal/fault/", "internal/home/", "internal/lint/"}, deckName},
	{"refusal", "a refusal built by hand: take a constructor of registry or a failure of fault", []string{"internal/", "cmd/"},
		[]string{"internal/registry/"}, refusalLit},
	{"secret", "a reference to a secret written out: take manifest.SecretRef or SecretPrefix", []string{"internal/", "cmd/"},
		[]string{"internal/manifest/", "internal/lint/"}, secretLit},
	{"exec", "a process started or a program looked up outside the sandbox: os/exec, LookPath and Setpgid are internal/sandbox's (sandbox.Find)", []string{"internal/", "cmd/"},
		[]string{"internal/sandbox/"}, execUse},
	{"client", "an HTTP client or transport built outside internal/egress: take one of egress", []string{"internal/", "cmd/"},
		[]string{"internal/egress/"}, clientLit},
	{"write", "a file written by hand: take home.WriteFile, home.Create or home.Fresh", []string{"internal/", "cmd/"},
		[]string{"internal/home/"}, fileWrite},
	{"read", "a file opened by its path outside home: a file of a folder a box writes is read by home.ReadIn or home.OpenRegular under a root",
		[]string{"internal/", "cmd/"}, []string{"internal/home/", "internal/lint/"}, fileRead},
	{"env", "the environment of deck read outside cmd/deck: take it from the config", []string{"internal/", "cmd/"},
		[]string{"cmd/deck/"}, envRead},
	{"failure", "an error that calls itself deck's by its text: take fault.New with its type, or a constructor of registry", []string{"internal/"},
		[]string{"internal/fault/", "internal/registry/", "internal/lint/"}, deckErr},
	{"topic", "a topic of events written out: take its constant of internal/events", []string{"internal/", "cmd/"},
		[]string{"internal/events/"}, topicLit},
}

// sqlRules are the conventions of the SQL of the store, by line.
var sqlRules = []struct {
	name, text string
	in         []string
	find       func(src string) []int
}{
	{"sql_now", "'now' outside the DEFAULT of a column: pass the time of deck's clock as sqlc.arg(now)",
		[]string{"internal/store/queries/", "internal/store/migrations/"}, sqlNow},
}

// topics are the topics of events, read from internal/events/topics.go of the root the rules check.
var topics []string

// Rules finds what breaks the conventions of the Go code and the SQL of the store under root outside the
// exceptions, and the exceptions that break nothing any more.
func Rules(root string) ([]Violation, error) {
	var err error
	if topics, err = readTopics(filepath.Join(root, "internal", "events", "topics.go")); err != nil {
		return nil, err
	}
	found := map[string][]string{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && slices.Contains(append(skipDirs, "tests"), d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if filepath.Ext(path) == ".sql" {
			return findSQL(path, rel, found)
		}
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
	named := map[string]string{}
	for _, r := range rules {
		named[r.name] = r.text
	}
	for _, r := range sqlRules {
		named[r.name] = r.text
	}
	var out []Violation
	for _, name := range slices.Sorted(maps.Keys(named)) {
		for _, at := range found[name] {
			if !slices.Contains(exceptions[name], at) {
				out = append(out, violationAt(at, named[name]))
			}
		}
		for _, at := range exceptions[name] {
			if !slices.Contains(found[name], at) {
				out = append(out, violationAt(at, fmt.Sprintf("an exception of rule %q that breaks nothing: take it out of the list", name)))
			}
		}
	}
	return out, nil
}

// findSQL adds to found what the rules of SQL find in a file.
func findSQL(path, rel string, found map[string][]string) error {
	var src []byte
	for _, r := range sqlRules {
		if !slices.ContainsFunc(r.in, func(p string) bool { return strings.HasPrefix(rel, p) }) {
			continue
		}
		if src == nil {
			var err error
			if src, err = os.ReadFile(path); err != nil {
				return err
			}
		}
		for _, line := range r.find(string(src)) {
			found[r.name] = append(found[r.name], rel+":"+strconv.Itoa(line))
		}
	}
	return nil
}

var defaultNow = regexp.MustCompile(`DEFAULT \(strftime\('[^']*', 'now'\)\)`)

// sqlNow are the lines that read 'now' anywhere but in the DEFAULT of a column.
func sqlNow(src string) []int {
	var out []int
	for i, line := range strings.Split(src, "\n") {
		line, _, _ = strings.Cut(line, "--")
		if strings.Contains(defaultNow.ReplaceAllString(line, ""), "'now'") {
			out = append(out, i+1)
		}
	}
	return out
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
			if v, err := strconv.Unquote(l.Value); err == nil && strings.HasPrefix(v, "deck.") && v != "deck.localhost" {
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

// secretLit are the lines of string literals, not tags, that hold a reference to a secret.
func secretLit(f *ast.File, fset *token.FileSet) []int {
	tags := map[*ast.BasicLit]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if fl, ok := n.(*ast.Field); ok && fl.Tag != nil {
			tags[fl.Tag] = true
		}
		return true
	})
	var out []int
	ast.Inspect(f, func(n ast.Node) bool {
		if l, ok := n.(*ast.BasicLit); ok && l.Kind == token.STRING && !tags[l] && strings.Contains(l.Value, "$secret.") {
			out = append(out, fset.Position(l.Pos()).Line)
		}
		return true
	})
	return out
}

// execUse are the lines that import os/exec, call exec.LookPath or set Setpgid.
func execUse(f *ast.File, fset *token.FileSet) []int {
	var out []int
	for _, im := range f.Imports {
		if p, _ := strconv.Unquote(im.Path.Value); p == "os/exec" {
			out = append(out, fset.Position(im.Pos()).Line)
		}
	}
	out = append(out, calls(f, fset, "os/exec", "LookPath")...)
	ast.Inspect(f, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == "Setpgid" {
			out = append(out, fset.Position(id.Pos()).Line)
		}
		return true
	})
	return out
}

// clientLit are the lines of composite literals of http.Client and http.Transport, and of the default client of
// net/http.
func clientLit(f *ast.File, fset *token.FileSet) []int {
	return append(composite(f, fset, "net/http", "Client", "Transport"),
		calls(f, fset, "net/http", "DefaultClient", "DefaultTransport", "Get", "Post", "Head", "PostForm")...)
}

// fileWrite are the lines that call os.Rename, os.CreateTemp or os.WriteFile.
func fileWrite(f *ast.File, fset *token.FileSet) []int {
	return calls(f, fset, "os", "Rename", "CreateTemp", "WriteFile")
}

// fileRead are the lines that open a file by its path to read it: os.ReadFile, os.Open, os.OpenFile with no flag
// of writing.
func fileRead(f *ast.File, fset *token.FileSet) []int {
	out := calls(f, fset, "os", "ReadFile", "Open")
	pkg := importName(f, "os")
	ast.Inspect(f, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok || len(c.Args) < 2 {
			return true
		}
		if s, ok := c.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "OpenFile" {
			if x, ok := s.X.(*ast.Ident); ok && pkg != "" && x.Name == pkg && x.Obj == nil {
				writes := false
				ast.Inspect(c.Args[1], func(m ast.Node) bool {
					if id, ok := m.(*ast.Ident); ok && slices.Contains([]string{"O_WRONLY", "O_RDWR", "O_CREATE", "O_APPEND"}, id.Name) {
						writes = true
					}
					return true
				})
				if !writes {
					out = append(out, fset.Position(c.Pos()).Line)
				}
			}
		}
		return true
	})
	return out
}

// envRead are the lines that read the environment of deck.
func envRead(f *ast.File, fset *token.FileSet) []int {
	return calls(f, fset, "os", "Getenv", "LookupEnv", "Environ")
}

// deckErr are the lines of fmt.Errorf and errors.New whose text begins "deck: ".
func deckErr(f *ast.File, fset *token.FileSet) []int {
	fmtName, errName := importName(f, "fmt"), importName(f, "errors")
	var out []int
	ast.Inspect(f, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok || len(c.Args) == 0 {
			return true
		}
		s, ok := c.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := s.X.(*ast.Ident)
		if !ok || !(x.Name == fmtName && s.Sel.Name == "Errorf" || x.Name == errName && s.Sel.Name == "New") {
			return true
		}
		if l, ok := c.Args[0].(*ast.BasicLit); ok && l.Kind == token.STRING {
			if v, err := strconv.Unquote(l.Value); err == nil && strings.HasPrefix(v, "deck: ") {
				out = append(out, fset.Position(c.Pos()).Line)
			}
		}
		return true
	})
	return out
}

// composite are the lines of composite literals of the named types of the package at path.
func composite(f *ast.File, fset *token.FileSet, path string, names ...string) []int {
	pkg := importName(f, path)
	if pkg == "" {
		return nil
	}
	var out []int
	ast.Inspect(f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CompositeLit); ok {
			if s, ok := c.Type.(*ast.SelectorExpr); ok && slices.Contains(names, s.Sel.Name) {
				if x, ok := s.X.(*ast.Ident); ok && x.Name == pkg {
					out = append(out, fset.Position(c.Pos()).Line)
				}
			}
		}
		return true
	})
	return out
}

// calls are the lines that name the functions of the package at path.
func calls(f *ast.File, fset *token.FileSet, path string, names ...string) []int {
	pkg := importName(f, path)
	if pkg == "" {
		return nil
	}
	var out []int
	ast.Inspect(f, func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok && slices.Contains(names, s.Sel.Name) {
			if x, ok := s.X.(*ast.Ident); ok && x.Name == pkg && x.Obj == nil {
				out = append(out, fset.Position(s.Pos()).Line)
			}
		}
		return true
	})
	return out
}

// readTopics are the values of the string constants of the file of topics.
func readTopics(path string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.CONST {
			for _, sp := range g.Specs {
				for _, v := range sp.(*ast.ValueSpec).Values {
					if v, ok := topicOf(v); ok {
						out = append(out, v)
					}
				}
			}
		}
	}
	return out, nil
}

// topicOf is the value of a string literal.
func topicOf(e ast.Expr) (string, bool) {
	l, ok := e.(*ast.BasicLit)
	if !ok || l.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(l.Value)
	return v, err == nil
}

// topicLit are the lines of topics written out where a topic goes: the topics of a read, the topic of an event, the
// topics a follower follows, a comparison with a topic and a list of topics.
func topicLit(f *ast.File, fset *token.FileSet) []int {
	var out []int
	add := func(e ast.Expr) {
		if v, ok := topicOf(e); ok && slices.Contains(topics, v) {
			out = append(out, fset.Position(e.Pos()).Line)
		}
	}
	isTopic := func(e ast.Expr) bool {
		switch x := e.(type) {
		case *ast.SelectorExpr:
			return x.Sel.Name == "Topic"
		case *ast.Ident:
			return x.Name == "topic"
		}
		return false
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			name := ""
			switch fn := x.Fun.(type) {
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			case *ast.Ident:
				name = fn.Name
			}
			if slices.Contains([]string{"On", "Follow", "Emit"}, name) {
				for _, a := range x.Args {
					ast.Inspect(a, func(n ast.Node) bool {
						if e, ok := n.(ast.Expr); ok {
							add(e)
						}
						return true
					})
				}
				return false
			}
		case *ast.KeyValueExpr:
			if k, ok := x.Key.(*ast.Ident); ok && k.Name == "Topic" {
				add(x.Value)
			}
		case *ast.AssignStmt:
			for i, l := range x.Lhs {
				if isTopic(l) && i < len(x.Rhs) {
					add(x.Rhs[i])
				}
			}
		case *ast.BinaryExpr:
			if x.Op == token.EQL || x.Op == token.NEQ {
				if isTopic(x.X) {
					add(x.Y)
				} else if isTopic(x.Y) {
					add(x.X)
				}
			}
		case *ast.SwitchStmt:
			if x.Tag != nil && isTopic(x.Tag) {
				for _, c := range x.Body.List {
					for _, e := range c.(*ast.CaseClause).List {
						add(e)
					}
				}
			}
		case *ast.CompositeLit:
			if len(x.Elts) > 1 && !slices.ContainsFunc(x.Elts, func(e ast.Expr) bool {
				v, ok := topicOf(e)
				return !ok || !slices.Contains(topics, v)
			}) {
				for _, e := range x.Elts {
					add(e)
				}
			}
		}
		return true
	})
	return out
}
