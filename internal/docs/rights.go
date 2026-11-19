package docs

import (
	"cmp"
	"fmt"
	"slices"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// RightRow is a right of a module on one subject: what it lets the module do in deck's words of the catalog, why in
// the module's own, and the owner's answer, empty while the module asks when it first needs it.
type RightRow struct {
	Project string `json:"project"`
	// Module is the title of the module and Title that of the right, in the owner's words: the headings of the two
	// views of the screen of rights.
	Module  string `json:"module"`
	Title   string `json:"title"`
	Right   string `json:"right" enum:"network|login|data|files|money|send|modules"`
	Who     string `json:"who,omitempty"`
	Subject string `json:"subject"`
	Says    string `json:"says"`
	Why     string `json:"why"`
	Answer  string `json:"answer" enum:"always|no|"`
	// At is when the owner answered; Used when the module last used the right on this subject, by the journal.
	At   string `json:"at,omitempty"`
	Used string `json:"used,omitempty"`
	// Declared is false for an answer to a right no manifest names: a command of deck or a site the browser acts on.
	Declared bool `json:"declared"`
	// Exit says the right is a way out of a turn: the network, money, sending in the owner's name, other modules.
	Exit bool `json:"exit"`
	// Key says the owner's always to it takes a fresh passkey, so enabling the module does not give it in advance.
	Key bool `json:"key"`
	// State is the answer in the owner's words with the day it was given, and that it was never used; Icon the icon of
	// the right.
	State string `json:"state"`
	Icon  string `json:"icon"`
	// Builtin marks a right of deck's own master, which deck grants and the owner does not answer.
	Builtin bool `json:"builtin,omitempty"`
	// Actions are the buttons a read gives a row: the first row of a module opens its report.
	Actions []map[string]any `json:"actions,omitempty"`
}

// RightRows are the rights m declares, a row a subject, with the owner's answers rows gives; then the answers to rights no
// manifest names.
func RightRows(p db.Project, m *manifest.Manifest, rows []db.RightGrant, tr func(string) string, usedAt Used) []RightRow {
	if usedAt == nil {
		usedAt = func(int64, string, string) string { return "" }
	}
	answer := func(right, who, subject string) (db.RightGrant, bool) {
		i := slices.IndexFunc(rows, func(r db.RightGrant) bool { return r.Name == right && r.Who == who && r.Subject == subject })
		if i < 0 {
			return db.RightGrant{}, false
		}
		return rows[i], true
	}
	var out []RightRow
	seen := map[[3]string]bool{}
	for _, d := range m.Rights {
		r, _ := manifest.RightOf(d.Right)
		for _, subject := range d.What {
			key := [3]string{d.Right, d.Whom(), subject}
			if seen[key] {
				continue
			}
			seen[key] = true
			n := registry.Need{Right: d.Right, Who: d.Whom(), Subject: subject}
			v := RightRow{Project: p.Name, Module: cmp.Or(m.Title, p.Name), Title: RightGroup(tr, d.Right), Right: d.Right, Who: d.Whom(), Subject: subject, Why: d.Why, Declared: true, Exit: r.Exit || d.Whom() == manifest.Agents || m.Posts(n),
				Says: m.SaysOn(tr, d.Right, d.Who, subject), Key: m.Keyed(n)}
			if g, ok := answer(d.Right, d.Whom(), subject); ok {
				v.Answer, v.At = g.Answer, g.CreatedAt
			}
			v.Used = usedAt(p.ID, d.Right, subject)
			out = append(out, v)
		}
	}
	for _, g := range rows {
		if seen[[3]string{g.Name, g.Who, g.Subject}] {
			continue
		}
		r, _ := manifest.RightOf(g.Name)
		n := registry.Need{Right: g.Name, Who: g.Who, Subject: g.Subject}
		out = append(out, RightRow{Project: p.Name, Module: cmp.Or(m.Title, p.Name), Title: RightGroup(tr, g.Name), Right: g.Name, Who: g.Who, Subject: g.Subject, Answer: g.Answer, At: g.CreatedAt, Exit: r.Exit || m.Posts(n),
			Says: m.SaysOn(tr, g.Name, g.Who, g.Subject), Key: m.Keyed(n), Used: usedAt(p.ID, g.Name, g.Subject)})
	}
	for i, v := range out {
		state := map[string]string{manifest.Always: "always", manifest.No: "you said no"}[v.Answer]
		switch {
		case p.Builtin == 1:
			state = "deck's own"
		case state != "":
		case v.Key:
			state = "asks you; always with your passkey"
		default:
			state = "asks you the first time"
		}
		out[i].State, out[i].Builtin = tr(state), p.Builtin == 1
		if v.At != "" && p.Builtin != 1 {
			out[i].State += " · " + fmt.Sprintf(tr("given %s"), v.At[:min(10, len(v.At))])
		}
		if v.Used == "" {
			out[i].State += " · " + tr("never used")
		}
		out[i].Icon = map[string]string{manifest.Network: "world", manifest.Login: "key", manifest.Data: "database", manifest.Files: "folder",
			manifest.Money: "coin", manifest.Send: "send", manifest.Modules: "apps"}[v.Right]
		if i == 0 {
			out[i].Actions = []map[string]any{{"verb": "report", "screen": "rights-report", "params": map[string]any{"project": p.Name}}}
		}
	}
	return out
}

// Used is when a module last used a right on a subject, empty when it did not.
type Used func(project int64, right, subject string) string

// RightGroup is a right of the catalog as the heading of its lines, in the owner's words.
func RightGroup(tr func(string) string, right string) string {
	if r, ok := manifest.RightOf(right); ok {
		return tr(r.Title)
	}
	return right
}
