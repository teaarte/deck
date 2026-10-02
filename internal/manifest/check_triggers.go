package manifest

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

func (m *Manifest) checkTriggers(fail func(string, ...any)) {
	lanes := map[string]bool{}
	if m.Board != nil {
		for _, l := range m.Board.Lanes {
			lanes[l.Name] = true
		}
	}
	after := map[string][]string{}
	for i, t := range m.Triggers {
		where := fmt.Sprintf("triggers[%d]", i)
		for j := range i {
			if reflect.DeepEqual(m.Triggers[j], t) {
				fail("%s: duplicate trigger for %q", where, t.Command)
			}
		}
		c := m.commands[t.Command]
		if c == nil {
			fail("%s: unknown command %q", where, t.Command)
		} else {
			if !slices.Contains(c.Callable(), "trigger") {
				fail("%s: command %q cannot be called by triggers", where, t.Command)
			}
			in := TriggerInput(t, 1)
			for _, f := range []string{"card", "process", "topic"} {
				if _, ok := in[f]; ok && !slices.Contains(schemaFields(c.Input), f) {
					fail("%s: command %q must declare input field %q to be triggered this way", where, t.Command, f)
				}
			}
			switch {
			case c.input != nil:
				if err := c.input.Validate(normalize(in)); err != nil {
					fail("%s: params do not match the input of %q: %v", where, t.Command, err)
				}
			case len(c.Input) == 0 && len(in) > 0:
				fail("%s: command %q takes no input; lane triggers pass card, after triggers pass process", where, t.Command)
			}
		}
		for j := range i {
			if u := m.Triggers[j]; t.On != "" && u.On == t.On {
				fail("%s: one trigger answers %s", where, t.On)
			}
			if u := m.Triggers[j]; u.Command == t.Command && u.Cron == t.Cron && u.Lane == t.Lane && u.After == t.After && u.On == t.On && !reflect.DeepEqual(u, t) {
				fail("%s: %q already has a trigger on the same %s", where, t.Command, triggerSource(t))
			}
		}
		switch {
		case t.Cron != "":
			if _, err := CronParser.Parse(t.Cron); err != nil {
				fail("%s: cron %q: %v", where, t.Cron, err)
			}
		case t.Lane != "":
			if !lanes[t.Lane] {
				fail("%s: unknown lane %q", where, t.Lane)
			}
		case t.After != "":
			if m.commands[t.After] == nil {
				fail("%s: unknown command %q in after", where, t.After)
			}
			after[t.After] = append(after[t.After], t.Command)
		}
	}
	if cycle := findCycle(after); cycle != nil {
		fail("triggers: commands trigger each other in a cycle: %s", strings.Join(cycle, " → "))
	}
}

func triggerSource(t Trigger) string {
	switch {
	case t.Cron != "":
		return fmt.Sprintf("cron %q", t.Cron)
	case t.Lane != "":
		return fmt.Sprintf("lane %q", t.Lane)
	default:
		return fmt.Sprintf("finish of %q", t.After)
	}
}

// TriggerInput is the command input a trigger passes: its params, plus the card for lane triggers and the
// finished process for after triggers.
func TriggerInput(t Trigger, ref int64) map[string]any {
	in := map[string]any{}
	for k, v := range t.Params {
		in[k] = v
	}
	switch {
	case t.Lane != "":
		in["card"] = ref
	case t.After != "" || t.On == OnFailure:
		in["process"] = ref
	case t.On == OnMessage:
		in["topic"] = ref
	}
	return in
}

// findCycle returns one cycle of the "finished → starts" graph, or nil.
func findCycle(edges map[string][]string) []string {
	state := map[string]int{}
	var stack []string
	var visit func(n string) []string
	visit = func(n string) []string {
		state[n] = 1
		stack = append(stack, n)
		for _, next := range edges[n] {
			switch state[next] {
			case 1:
				i := slices.Index(stack, next)
				return append(append([]string{}, stack[i:]...), next)
			case 0:
				if c := visit(next); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[n] = 2
		return nil
	}
	keys := make([]string, 0, len(edges))
	for k := range edges {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if state[k] == 0 {
			if c := visit(k); c != nil {
				return c
			}
		}
	}
	return nil
}
