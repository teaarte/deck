package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// checkVersion refuses a manifest that is no JSON object or not of the current format.
func checkVersion(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return fmt.Errorf("manifest: %w", where(raw, err))
	}
	if n, _ := m["version"].(json.Number); n.String() == fmt.Sprint(FormatVersion) {
		return nil
	}
	got := fmt.Sprint(m["version"])
	if t, ok := m["version"].(string); ok {
		got = fmt.Sprintf("the text %q", t)
	}
	return fmt.Errorf("manifest: version must be the number %d, got %s", FormatVersion, got)
}

// where adds to a JSON error the line and column it stopped at and the text around.
func where(raw []byte, err error) error {
	var off int64
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		off = syn.Offset
	case errors.As(err, &typ):
		off = typ.Offset
	default:
		return err
	}
	off = min(off, int64(len(raw)))
	before := raw[:off]
	line := bytes.Count(before, []byte("\n")) + 1
	col := int(off) - bytes.LastIndexByte(before, '\n')
	from, to := max(0, int(off)-40), min(len(raw), int(off)+20)
	return fmt.Errorf("%w at line %d, column %d: %q", err, line, col, raw[from:to])
}
