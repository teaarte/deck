package registry

import (
	"encoding/json"
	"reflect"
	"strings"
)

var rawType = reflect.TypeFor[json.RawMessage]()

func resultSchema(t reflect.Type) map[string]any {
	if t == rawType {
		return map[string]any{}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return resultSchema(t.Elem())
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64, reflect.Int32:
		return map[string]any{"type": "integer"}
	case reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": resultSchema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object"}
	case reflect.Struct:
		props := map[string]any{}
		resultFields(t, props)
		return map[string]any{"type": "object", "properties": props}
	}
	return map[string]any{}
}

func resultFields(t reflect.Type, props map[string]any) {
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			resultFields(f.Type, props)
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !f.IsExported() || name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		props[name] = resultSchema(f.Type)
	}
}
