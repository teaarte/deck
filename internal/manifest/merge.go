package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Conflict is a path of a manifest both sides changed, each its own way, with the value of base and of each side;
// a missing value is nil.
type Conflict struct {
	Path             string
	Base, Edit, Next json.RawMessage
}

// Merge3 puts the change from base to edit into next by parts: objects by keys, sets by values, named entries by name,
// the rest whole; a path both changed apart keeps next and is a conflict. Keys keep the order of next, edit's go last.
func Merge3(edit, base, next []byte) ([]byte, []Conflict, error) {
	var vs [3]any
	for i, raw := range [][]byte{edit, base, next} {
		v, err := decodeOrdered(raw)
		if err != nil {
			return nil, nil, err
		}
		vs[i] = v
	}
	m := &merger{}
	out, _ := m.merge("", vs[0], vs[1], vs[2], true, true, true)
	var buf bytes.Buffer
	encodeOrdered(&buf, out, "")
	return []byte(Unescaped(buf.String())), m.conflicts, nil
}

type merger struct{ conflicts []Conflict }

// merge merges one value; has* say whether the value is there at all, ok whether the result is.
func (m *merger) merge(path string, edit, base, next any, hasEdit, hasBase, hasNext bool) (any, bool) {
	switch {
	case same(edit, base, hasEdit, hasBase):
		return next, hasNext
	case same(next, base, hasNext, hasBase), same(edit, next, hasEdit, hasNext):
		return edit, hasEdit
	}
	if hasEdit && hasNext {
		eo, eok := edit.(*object)
		no, nok := next.(*object)
		bo, bok := base.(*object)
		if !hasBase {
			bo, bok = &object{vals: map[string]any{}}, true
		}
		if eok && nok && bok {
			return m.objects(path, eo, bo, no), true
		}
		ea, eok := edit.([]any)
		na, nok := next.([]any)
		ba, bok := base.([]any)
		if !hasBase {
			ba, bok = nil, true
		}
		if eok && nok && bok {
			if setPath(path) {
				return mergeSet(ea, ba, na), true
			}
			if key := keyOf(path); key != "" && keyed(ea, key) && keyed(ba, key) && keyed(na, key) {
				return m.named(path, key, ea, ba, na), true
			}
		}
	}
	m.conflicts = append(m.conflicts, Conflict{Path: cmpPath(path), Base: raw(base, hasBase), Edit: raw(edit, hasEdit), Next: raw(next, hasNext)})
	return next, hasNext
}

func (m *merger) objects(path string, edit, base, next *object) *object {
	out := &object{vals: map[string]any{}}
	put := func(k string) {
		e, he := edit.vals[k]
		b, hb := base.vals[k]
		n, hn := next.vals[k]
		if v, ok := m.merge(path+"/"+escape(k), e, b, n, he, hb, hn); ok {
			out.keys, out.vals[k] = append(out.keys, k), v
		}
	}
	for _, k := range next.keys {
		put(k)
	}
	for _, k := range edit.keys {
		if _, inNext := next.vals[k]; !inNext {
			put(k)
		}
	}
	return out
}

// named merges arrays of entries named by key as objects by their names, in the order of next.
func (m *merger) named(path, key string, edit, base, next []any) []any {
	asObject := func(a []any) *object {
		o := &object{vals: map[string]any{}}
		for _, v := range a {
			name := fmt.Sprint(v.(*object).vals[key])
			o.keys, o.vals[name] = append(o.keys, name), v
		}
		return o
	}
	merged := m.objects(path, asObject(edit), asObject(base), asObject(next))
	out := make([]any, 0, len(merged.keys))
	for _, k := range merged.keys {
		out = append(out, merged.vals[k])
	}
	return out
}

// mergeSet is next with the values edit added to base and without those edit took from it.
func mergeSet(edit, base, next []any) []any {
	has := func(a []any, v any) bool {
		return slices.ContainsFunc(a, func(x any) bool { return equal(x, v) })
	}
	var out []any
	for _, v := range next {
		if has(edit, v) || !has(base, v) {
			out = append(out, v)
		}
	}
	for _, v := range edit {
		if !has(base, v) && !has(out, v) {
			out = append(out, v)
		}
	}
	if out == nil {
		out = []any{}
	}
	return out
}

func same(a, b any, hasA, hasB bool) bool {
	return hasA == hasB && (!hasA || equal(a, b))
}

// equal compares JSON values by what they mean: objects whatever the order of their keys, numbers by value.
func equal(a, b any) bool {
	switch x := a.(type) {
	case *object:
		y, ok := b.(*object)
		if !ok || len(x.vals) != len(y.vals) {
			return false
		}
		for k, v := range x.vals {
			if w, ok := y.vals[k]; !ok || !equal(v, w) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		return ok && slices.EqualFunc(x, y, equal)
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		f, err1 := x.Float64()
		g, err2 := y.Float64()
		return err1 == nil && err2 == nil && f == g || x == y
	}
	return a == b
}

// keyed reports whether every entry of a is an object with a distinct value of key.
func keyed(a []any, key string) bool {
	seen := map[string]bool{}
	for _, v := range a {
		o, ok := v.(*object)
		if !ok {
			return false
		}
		name, ok := o.vals[key].(string)
		if !ok || seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}

func raw(v any, has bool) json.RawMessage {
	if !has {
		return nil
	}
	var buf bytes.Buffer
	encodeOrdered(&buf, v, "")
	return json.RawMessage(buf.Bytes())
}

func escape(k string) string { return strings.NewReplacer("~", "~0", "/", "~1").Replace(k) }

func cmpPath(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

// sets are the arrays of a manifest whose order means nothing, by path with "*" for any name: a merge adds and
// takes their values one by one.
var sets = []string{"/allowed_external", "/models", "/writes", "/domains/read", "/domains/act", "/reads/*/collections",
	"/reads/*/documents", "/commands/*/callers", "/commands/*/requires", "/commands/*/executor/calls",
	"/commands/*/executor/skills", "/collections/*/immutable", "/tools/mcp/servers/*/toolFilter"}

// named are the arrays of a manifest whose entries a key names, by path: a merge goes entry by entry.
var named = map[string]string{"/collections": "name", "/commands": "name", "/scripts": "name", "/scripts/*/programs": "name",
	"/screens": "name", "/tools/http": "name", "/reads": "project"}

// setPath reports whether the array at path is a set.
func setPath(path string) bool {
	return slices.ContainsFunc(sets, func(p string) bool { return pathIs(path, p) })
}

// keyOf is the key that names the entries of the array at path, empty for an array of other values.
func keyOf(path string) string {
	for p, key := range named {
		if pathIs(path, p) {
			return key
		}
	}
	return ""
}

// pathIs reports whether path is of pattern, "*" standing for any one part.
func pathIs(path, pattern string) bool {
	a, b := strings.Split(path, "/"), strings.Split(pattern, "/")
	if len(a) != len(b) {
		return false
	}
	for i := range b {
		if b[i] != "*" && b[i] != a[i] {
			return false
		}
	}
	return true
}
