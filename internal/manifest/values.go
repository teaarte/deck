package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/builtin"
	"github.com/expr-lang/expr/parser"
	"github.com/expr-lang/expr/vm"
	"github.com/santhosh-tekuri/jsonschema/v6"
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
	return expr.Compile(src, expr.AllowUndefinedVariables())
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

func Eval(src string, env map[string]any) (any, error) {
	p, err := compileExpr(src, nil)
	if err != nil {
		return nil, err
	}
	return expr.Run(p, env)
}

func compileSchema(name string, raw json.RawMessage) (*jsonschema.Schema, error) {
	if len(raw) == 0 {
		return nil, errors.New("schema is required")
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	url := "mem://" + name + ".json"
	if err := c.AddResource(url, doc); err != nil {
		return nil, err
	}
	return c.Compile(url)
}

// schemaFields lists top-level properties of an object schema; nil when it declares none.
func schemaFields(raw json.RawMessage) []string {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(raw, &s) != nil || len(s.Properties) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.Properties))
	for k := range s.Properties {
		out = append(out, k)
	}
	return out
}

// DecodeObject parses one JSON object, keeping integers exact as int64.
func DecodeObject(b []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("not a JSON object")
	}
	if dec.More() {
		return nil, errors.New("extra data after the JSON object")
	}
	out, err := exactNumbers(m)
	if err != nil {
		return nil, err
	}
	return out.(map[string]any), nil
}

// exactNumbers turns integers into int64 and refuses integer literals that do not fit instead of rounding them.
func exactNumbers(v any) (any, error) {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, nil
		}
		if r, ok := new(big.Rat).SetString(x.String()); ok && r.IsInt() && r.Num().IsInt64() {
			return r.Num().Int64(), nil
		}
		if r, ok := new(big.Rat).SetString(x.String()); ok && r.IsInt() {
			return nil, fmt.Errorf("whole number %s does not fit in 64 bits", x)
		}
		f, _ := x.Float64()
		return f, nil
	case map[string]any:
		for k, e := range x {
			n, err := exactNumbers(e)
			if err != nil {
				return nil, err
			}
			x[k] = n
		}
	case []any:
		for i, e := range x {
			n, err := exactNumbers(e)
			if err != nil {
				return nil, err
			}
			x[i] = n
		}
	}
	return v, nil
}

// normalize round-trips a value through JSON so numbers and maps have the types the validator expects.
func normalize(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	out, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return v
	}
	return out
}

// DecodeValue parses one JSON value, keeping numbers as they were written.
func DecodeValue(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("extra data after the JSON value")
	}
	return v, nil
}

// DecodeExact parses one JSON value with integers as int64, like a record.
func DecodeExact(b []byte) (any, error) {
	v, err := DecodeValue(b)
	if err != nil {
		return nil, err
	}
	return exactNumbers(v)
}

// CheckValue validates a value against a JSON Schema.
func CheckValue(schema json.RawMessage, v any) error {
	s, err := compileSchema("value", schema)
	if err != nil {
		return err
	}
	return s.Validate(normalize(v))
}
