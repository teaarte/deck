package manifest

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/builtin"
	"github.com/expr-lang/expr/file"
	"github.com/expr-lang/expr/parser"
	"github.com/expr-lang/expr/vm"
	"github.com/expr-lang/expr/vm/runtime"
)

// compileExpr compiles an expression; with fields set, names outside them are errors.
func compileExpr(src string, fields []string) (*vm.Program, error) {
	if strings.TrimSpace(src) == "" {
		return nil, errors.New("expression is empty")
	}
	if fields != nil {
		tree, err := parser.Parse(src)
		if err != nil {
			return nil, err
		}
		v := &names{callees: map[*ast.IdentifierNode]bool{}, lets: map[string]bool{}}
		ast.Walk(&tree.Node, v)
		for name := range v.lets {
			if slices.Contains(fields, name) {
				return nil, fmt.Errorf("variable %q hides the field of the same name", name)
			}
		}
		for _, id := range v.ids {
			if !v.callees[id] && !v.lets[id.Value] && !slices.Contains(fields, id.Value) {
				return nil, fmt.Errorf("unknown field %q; fields are %s", id.Value, strings.Join(fields, ", "))
			}
		}
	}
	tree, err := parser.Parse(src)
	if err != nil {
		return nil, err
	}
	v := &names{callees: map[*ast.IdentifierNode]bool{}, lets: map[string]bool{}}
	ast.Walk(&tree.Node, v)
	for id := range v.callees {
		if _, ok := builtin.Index[id.Value]; !ok {
			return nil, fmt.Errorf("unknown function %q", id.Value)
		}
	}
	return expr.Compile(src, expr.AllowUndefinedVariables(), expr.Function(sliceFn, slice), expr.Patch(chars{}))
}

// sliceFn slices in place of [a:b]; a name no expression can call.
const sliceFn = "$slice"

// chars makes [a:b] slice a text by characters, as len counts them.
type chars struct{}

func (chars) Visit(n *ast.Node) {
	s, ok := (*n).(*ast.SliceNode)
	if !ok {
		return
	}
	args := []ast.Node{s.Node, s.From, s.To}
	for i, a := range args {
		if a == nil {
			args[i] = &ast.NilNode{}
		}
	}
	ast.Patch(n, &ast.CallNode{Callee: &ast.IdentifierNode{Value: sliceFn}, Arguments: args})
}

func slice(p ...any) (any, error) {
	v := p[0]
	if s, ok := v.(string); ok {
		v = []rune(s)
	}
	from, to := p[1], p[2]
	if from == nil {
		from = 0
	}
	if to == nil {
		to = runtime.Len(v)
	}
	out := runtime.Slice(v, from, to)
	if r, ok := out.([]rune); ok {
		return string(r), nil
	}
	return out, nil
}

// compileField compiles an expression over record fields; variables need known fields so they cannot hide one.
func compileField(src string, fields []string) (*vm.Program, error) {
	if fields == nil && hasLet(src) {
		return nil, errors.New("variables (let) need a schema that lists its properties")
	}
	return compileExpr(src, fields)
}

// hasLet reports whether an expression declares variables.
func hasLet(src string) bool {
	tree, err := parser.Parse(src)
	if err != nil {
		return false
	}
	v := &names{callees: map[*ast.IdentifierNode]bool{}, lets: map[string]bool{}}
	ast.Walk(&tree.Node, v)
	return len(v.lets) > 0
}

// OneField is the one field an expression reads, empty when it reads none or more than one.
func OneField(src string) string {
	if f := exprFields(src, nil); len(f) == 1 {
		return f[0]
	}
	return ""
}

// exprFields lists the fields an expression reads, out of the given ones; any name when fields is nil.
func exprFields(src string, fields []string) []string {
	tree, err := parser.Parse(src)
	if err != nil {
		return nil
	}
	v := &names{callees: map[*ast.IdentifierNode]bool{}, lets: map[string]bool{}}
	ast.Walk(&tree.Node, v)
	var out []string
	for _, id := range v.ids {
		if !v.callees[id] && !v.lets[id.Value] && (fields == nil || slices.Contains(fields, id.Value)) && !slices.Contains(out, id.Value) {
			out = append(out, id.Value)
		}
	}
	return out
}

type names struct {
	ids     []*ast.IdentifierNode
	callees map[*ast.IdentifierNode]bool
	lets    map[string]bool
}

func (v *names) Visit(n *ast.Node) {
	switch x := (*n).(type) {
	case *ast.IdentifierNode:
		if x.Value != "$env" {
			v.ids = append(v.ids, x)
		}
	case *ast.CallNode:
		if id, ok := x.Callee.(*ast.IdentifierNode); ok {
			v.callees[id] = true
		}
	case *ast.VariableDeclaratorNode:
		v.lets[x.Name] = true
	}
}

// Filter compiles a where: an expression that is true for what it keeps.
func Filter(src string) (func(env map[string]any) (bool, error), error) {
	p, err := compileExpr(src, nil)
	if err != nil {
		return nil, err
	}
	return func(env map[string]any) (bool, error) {
		v, err := expr.Run(p, env)
		if err != nil {
			return false, err
		}
		ok, isBool := v.(bool)
		if !isBool {
			return false, fmt.Errorf("gives %T, not true or false", v)
		}
		return ok, nil
	}, nil
}

// Eval runs an expression over env; a failure names the expression and what went wrong, without the caret drawing.
func Eval(src string, env map[string]any) (any, error) {
	p, err := compileExpr(src, nil)
	if err != nil {
		return nil, err
	}
	out, err := expr.Run(p, env)
	if fe := (*file.Error)(nil); errors.As(err, &fe) {
		return nil, fmt.Errorf("%s: %s", src, fe.Message)
	}
	return out, err
}
