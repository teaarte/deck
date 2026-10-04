package docs

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"

	"deck2/internal/registry"
)

// Patch applies a JSON Patch to a JSON text. A replace changes only the value at its path, so the text around
// it stays as written; a patch of tests only keeps the text; any other operation indents the whole document anew.
func Patch(text, patch string) (string, error) {
	p, err := jsonpatch.DecodePatch([]byte(patch))
	if err != nil {
		return "", registry.Invalid("deck: patch: %v", err)
	}
	var raw []struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal([]byte(patch), &raw); err != nil || len(raw) != len(p) {
		return "", registry.Invalid("deck: patch: %v", err)
	}
	if onlyTests(p) {
		if _, err := p.Apply([]byte(text)); err != nil {
			return "", registry.Invalid("deck: patch: %v", err)
		}
		return text, nil
	}
	out := []byte(text)
	for i, op := range p {
		path, err := op.Path()
		if err != nil {
			return "", registry.Invalid("deck: patch: %v", err)
		}
		start, end, found := valueSpan(out, path)
		if op.Kind() != "replace" || !found {
			whole, err := p.ApplyIndent([]byte(text), "  ")
			if err != nil {
				return "", registry.Invalid("deck: patch: %v", err)
			}
			return string(whole), nil
		}
		var value bytes.Buffer
		if err := json.Compact(&value, raw[i].Value); err != nil {
			return "", registry.Invalid("deck: patch: %v", err)
		}
		out = append(out[:start:start], append(value.Bytes(), out[end:]...)...)
	}
	return string(out), nil
}

func onlyTests(p jsonpatch.Patch) bool {
	for _, op := range p {
		if op.Kind() != "test" {
			return false
		}
	}
	return len(p) > 0
}

// valueSpan is where the value at a JSON pointer starts and ends in a JSON text.
func valueSpan(text []byte, pointer string) (int, int, bool) {
	if pointer == "" {
		return 0, 0, false
	}
	dec := json.NewDecoder(bytes.NewReader(text))
	for _, part := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		part = strings.NewReplacer("~1", "/", "~0", "~").Replace(part)
		t, err := dec.Token()
		if err != nil {
			return 0, 0, false
		}
		if found, err := enter(dec, t, part); err != nil || !found {
			return 0, 0, false
		}
	}
	start := next(text, int(dec.InputOffset()))
	var raw json.RawMessage
	if dec.Decode(&raw) != nil {
		return 0, 0, false
	}
	return start, int(dec.InputOffset()), true
}

// enter moves the decoder inside the object or array that starts with t to just before the value named part.
func enter(dec *json.Decoder, t json.Token, part string) (bool, error) {
	var raw json.RawMessage
	switch t {
	case json.Delim('{'):
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return false, err
			}
			if key == part {
				return true, nil
			}
			if err := dec.Decode(&raw); err != nil {
				return false, err
			}
		}
	case json.Delim('['):
		n, err := strconv.Atoi(part)
		if err != nil {
			return false, nil
		}
		for i := 0; dec.More(); i++ {
			if i == n {
				return true, nil
			}
			if err := dec.Decode(&raw); err != nil {
				return false, err
			}
		}
	default:
		return false, errors.New("not a container")
	}
	return false, nil
}

// next is the first offset from i that is not space or a separator: where the next value starts.
func next(text []byte, i int) int {
	for i < len(text) && strings.IndexByte(" \t\r\n:,", text[i]) >= 0 {
		i++
	}
	return i
}
