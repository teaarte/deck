package registry

import "testing"

func TestPermits(t *testing.T) {
	for _, row := range []struct {
		patterns []string
		name     string
		permits  bool
		what     string
	}{
		{[]string{"deck.record_get"}, "deck.record_get", true, "the name itself"},
		{[]string{"deck.record_get"}, "deck.record_gets", false, "a longer name"},
		{[]string{"deck.record_get"}, "deck.record_ge", false, "a shorter name"},
		{[]string{"dec.*"}, "dec.add", true, "a command under the module"},
		{[]string{"dec.*"}, "dec.sub.add", true, "a command deeper under it"},
		{[]string{"dec.*"}, "decx.add", false, "a module that starts the same"},
		{[]string{"dec.*"}, "dec", false, "the module name alone"},
		{[]string{"dec"}, "dec.add", false, "a module name without .*"},
		{[]string{"*"}, "deck.project_delete", true, "the wildcard"},
		{[]string{"*.add"}, "dec.add", false, "a wildcard in front"},
		{[]string{"dec*"}, "dec.add", false, "a star without the dot"},
		{[]string{"DEC.*"}, "dec.add", false, "another case"},
		{nil, "dec.add", false, "an empty list"},
		{[]string{"notes.get", "dec.*"}, "dec.add", true, "one of several"},
	} {
		if got := Permits(row.patterns, row.name); got != row.permits {
			t.Errorf("Permits(%q, %q) = %v, want %v: %s", row.patterns, row.name, got, row.permits, row.what)
		}
	}
}
