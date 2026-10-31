package manifest

import (
	"fmt"
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
func Resolve(v, typ string, env map[string]any) (any, error) {
	if v == "" {
		return nil, nil
	}
	if e, ok := strings.CutPrefix(v, "="); ok {
		out, err := Eval(e, env)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", v, err)
		}
		return out, nil
	}
	return Typed(v, typ), nil
}

// Holds is whether a when gave true; nil holds not, and a value that is no boolean is an error.
func Holds(v any) (bool, error) {
	switch v := v.(type) {
	case nil:
		return false, nil
	case bool:
		return v, nil
	}
	return false, fmt.Errorf("gives %v, not true or false", v)
}
