package manifest

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// Typed is text a manifest or an address gives, turned into the JSON type of the field it fills.
func Typed(v, typ string) any {
	switch typ {
	case "integer":
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	case "number":
		if f, err := strconv.ParseFloat(v, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f
		}
	case "boolean":
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return v
}

// Resolve is a value a manifest gives a field of the type typ: fixed text in that type, or =<expression> over env,
// where param holds the parameters of the screen.
func Resolve(v, typ string, env map[string]any) any {
	if v == "" {
		return nil
	}
	if e, ok := strings.CutPrefix(v, "="); ok {
		out, _ := Eval(e, env)
		return out
	}
	return Typed(v, typ)
}

// FieldTypes are the JSON types of the fields of an object schema, "any" for a field without one.
func FieldTypes(schema json.RawMessage) map[string]string {
	var in struct {
		Properties map[string]struct{ Type any } `json:"properties"`
	}
	_ = json.Unmarshal(schema, &in)
	out := map[string]string{}
	for k, p := range in.Properties {
		out[k], _ = p.Type.(string)
		if out[k] == "" {
			out[k] = "any"
		}
	}
	return out
}
