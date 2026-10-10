package manifest

import (
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/expr-lang/expr"
)

// Validate checks a record against the collection schema.
func (c *Collection) Validate(data map[string]any) error {
	if err := c.schema.Validate(normalize(data)); err != nil {
		return fmt.Errorf("record does not match schema of %q: %w", c.Name, err)
	}
	return nil
}

// RecordKey computes the uniqueness key of a record after validating it against the collection schema.
func (c *Collection) RecordKey(data map[string]any) (string, error) {
	if err := c.Validate(data); err != nil {
		return "", err
	}
	return c.KeyOf(data)
}

// KeyOf computes the key without schema validation.
func (c *Collection) KeyOf(data map[string]any) (string, error) {
	out, err := expr.Run(c.keyProg, data)
	if err != nil {
		return "", fmt.Errorf("key of %q: %w", c.Name, err)
	}
	var key string
	switch v := out.(type) {
	case string:
		key = v
	case int, int64, bool:
		key = fmt.Sprint(v)
	case float64:
		key = strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return "", fmt.Errorf("key of %q must be a string or number, got %T", c.Name, out)
	}
	if key == "" {
		return "", fmt.Errorf("key of %q is empty", c.Name)
	}
	return key, nil
}

// RecordTitle is the display name of a record; empty when the collection declares none.
func (c *Collection) RecordTitle(data map[string]any) string {
	if c.displayProg == nil {
		return ""
	}
	out, err := expr.Run(c.displayProg, data)
	if err != nil || out == nil {
		return ""
	}
	return fmt.Sprint(out)
}

// FolderOf is the folder of a record in the data folder, relative and inside it; empty when the collection names
// none or the record gives no path.
func (c *Collection) FolderOf(data map[string]any) string {
	if c.folderProg == nil {
		return ""
	}
	out, err := expr.Run(c.folderProg, data)
	if err != nil || out == nil {
		return ""
	}
	return strings.TrimPrefix(filepath.Clean("/"+fmt.Sprint(out)), "/")
}

// ParentKey is the key of the record of the parent collection a record belongs to; false when it names none.
func (c *Collection) ParentKey(data map[string]any) (string, bool) {
	if c.parentProg == nil {
		return "", false
	}
	out, err := expr.Run(c.parentProg, data)
	if err != nil || out == nil || fmt.Sprint(out) == "" {
		return "", false
	}
	return fmt.Sprint(out), true
}

// Children are the collections whose records belong to a record of the collection.
func (m *Manifest) Children(collection string) []*Collection {
	var out []*Collection
	for i := range m.Collections {
		if p := m.Collections[i].Parent; p != nil && p.Collection == collection {
			out = append(out, &m.Collections[i])
		}
	}
	return out
}

// CheckImmutable refuses changes of immutable and key fields between the stored and the new record.
func (c *Collection) CheckImmutable(old, next map[string]any) error {
	for _, f := range append(slices.Clone(c.Immutable), c.keyFields...) {
		if !reflect.DeepEqual(normalize(old[f]), normalize(next[f])) {
			return fmt.Errorf("field %q of %q cannot change after the record is created", f, c.Name)
		}
	}
	return nil
}
