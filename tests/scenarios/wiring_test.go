package scenarios

import (
	"reflect"
	"strings"
	"testing"
)

// TestNewLeavesNoServiceHalfBuilt: after New every service of deck has each dependency it names, so none is
// filled in later by hand.
func TestNewLeavesNoServiceHalfBuilt(t *testing.T) {
	e := newEnv(t, nil)
	app := reflect.ValueOf(e.App).Elem()
	var missing []string
	for i := range app.NumField() {
		f, name := app.Field(i), app.Type().Field(i).Name
		if !app.Type().Field(i).IsExported() {
			continue
		}
		if f.Kind() == reflect.Pointer && f.IsNil() {
			missing = append(missing, name)
			continue
		}
		if f.Kind() != reflect.Pointer || f.Elem().Kind() != reflect.Struct || !strings.HasPrefix(f.Elem().Type().PkgPath(), "deck2/internal/app/") {
			continue
		}
		s := f.Elem()
		for j := range s.NumField() {
			sf := s.Type().Field(j)
			if !sf.IsExported() {
				continue
			}
			switch v := s.Field(j); v.Kind() {
			case reflect.Func, reflect.Pointer, reflect.Interface:
				if v.IsNil() {
					missing = append(missing, name+"."+sf.Name)
				}
			}
		}
	}
	if len(missing) > 0 {
		t.Fatalf("New leaves dependencies of services unset: %v", missing)
	}
}
