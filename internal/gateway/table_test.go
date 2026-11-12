package gateway

import "testing"

func Test_decide(t *testing.T) {
	declared := Facts{Needs: true, Declared: true}
	with := func(f Facts, edit func(*Facts)) Facts {
		edit(&f)
		return f
	}
	for _, row := range []struct {
		facts Facts
		name  string
		does  string
		what  string
	}{
		{Facts{}, "in_deck", DoRun, "a call that needs no right"},
		{Facts{Owner: true, Needs: true}, "owner", DoRun, "the owner's own call"},
		{Facts{Owner: true, Needs: true, Answer: "no"}, "owner", DoRun, "the owner's call past a no of the module"},
		{Facts{Needs: true, Cleared: true}, "cleared", DoRun, "a call the owner's answer let through"},
		{Facts{Needs: true, Cleared: true, Draft: true}, "cleared", DoRun, "a draft's call the owner allowed"},
		{Facts{Needs: true, Refused: "bad input"}, "refused", DoRefuse, "the command refuses the input"},
		{Facts{Needs: true, Refused: "bad input", Answer: "always", Declared: true}, "refused", DoRefuse, "a refusal past always"},
		{Facts{Needs: true, Draft: true, Declared: true, Answer: "always"}, "draft", DoRefuse, "a draft holds no answer"},
		{Facts{Needs: true, Draft: true, Looks: true}, "draft_tool", DoAsk, "a tool of a draft asks"},
		{Facts{Needs: true, Draft: true, Looks: true, Pressed: true}, "draft_tool", DoAsk, "a press in a trial does not count"},
		{Facts{Needs: true}, "undeclared", DoRefuse, "a right the module did not declare"},
		{Facts{Needs: true, Answer: "always"}, "undeclared", DoRefuse, "always on an undeclared right"},
		{Facts{Needs: true, Pressed: true}, "undeclared", DoRefuse, "a press of an undeclared right"},
		{with(declared, func(f *Facts) { f.Answer = "no" }), "denied", DoRefuse, "the owner said no"},
		{with(declared, func(f *Facts) { f.Answer = "no"; f.Pressed = true }), "denied", DoRefuse, "a press past a no"},
		{with(declared, func(f *Facts) { f.Answer = "always" }), "always", DoRun, "always, not shown"},
		{with(declared, func(f *Facts) { f.Answer = "always"; f.Shown = true }), "always", DoShow, "always, shown after"},
		{with(declared, func(f *Facts) { f.Answer = "always"; f.Asked = true }), "ask", DoAsk, "always does not meet an asked need"},
		{with(declared, func(f *Facts) { f.Answer = Work }), "for_work", DoRun, "for the work of this process"},
		{with(declared, func(f *Facts) { f.Answer = Work; f.Shown = true }), "for_work", DoShow, "for the work, shown after"},
		{with(declared, func(f *Facts) { f.Answer = Work; f.Asked = true }), "ask", DoAsk, "for the work does not meet an asked need"},
		{with(declared, func(f *Facts) { f.Pressed = true; f.Shown = true }), "pressed", DoShow, "the owner's press named it"},
		{with(declared, func(f *Facts) { f.Pressed = true; f.Asked = true }), "ask", DoAsk, "a press does not meet an asked need"},
		{with(declared, func(f *Facts) { f.InForce = true }), "in_force", DoUnchanged, "what it makes is so already"},
		{with(declared, func(f *Facts) { f.InForce = true; f.Answer = "always" }), "always", DoRun, "always runs one in force"},
		{declared, "ask", DoAsk, "nothing answered: ask"},
		{with(declared, func(f *Facts) { f.Looks = true }), "ask", DoAsk, "a tool, nothing answered: ask"},
	} {
		got := decide(row.facts)
		if got.Name != row.name || got.Does != row.does || got.Text == "" {
			t.Errorf("decide(%+v) = %s/%s, want %s/%s: %s", row.facts, got.Name, got.Does, row.name, row.does, row.what)
		}
	}
}
