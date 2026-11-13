package registry

import (
	"testing"
	"time"
)

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

func TestTouched(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		kind    string
		touched time.Duration
		none    bool
		fresh   bool
		what    string
	}{
		{Owner, 0, false, true, "the owner touched the passkey just now"},
		{Owner, time.Minute, false, true, "a minute ago"},
		{Owner, 4*time.Minute + 59*time.Second, false, true, "just under five minutes ago"},
		{Owner, 5 * time.Minute, false, true, "five minutes ago, the end of the batch"},
		{Owner, 5*time.Minute + time.Second, false, false, "a second past five minutes"},
		{Owner, time.Hour, false, false, "an hour ago"},
		{Owner, 7 * 24 * time.Hour, false, false, "a week ago, the longest a session lasts"},
		{Owner, -time.Second, false, false, "a touch in the future, a clock that went back"},
		{Owner, 0, true, false, "a session no ceremony began, or a call of the owner deck makes itself"},
		{Master, 0, false, false, "the master, whatever its time"},
		{Agent, 0, false, false, "an agent"},
		{Trigger, 0, false, false, "a trigger"},
		{Token, 0, false, false, "an automation token"},
	} {
		a := Actor{Kind: row.kind}
		if !row.none {
			a.Passkey = now.Add(-row.touched)
		}
		if got := Touched(a, now); got != row.fresh {
			t.Errorf("Touched(%s, %v ago) = %v, want %v: %s", row.kind, row.touched, got, row.fresh, row.what)
		}
	}
}
