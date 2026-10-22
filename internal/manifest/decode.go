package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

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
