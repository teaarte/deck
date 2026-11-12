package project

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"strings"

	"deck2/internal/access"
	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/words"
)

// rightView is a right of a module on one subject: what it lets the module do in deck's words of the catalog, why in
// the module's own, and the owner's answer, empty while the module asks when it first needs it.
type rightView struct {
	Project string `json:"project"`
	Right   string `json:"right" enum:"network|login|data|files|money|send|modules"`
	Who     string `json:"who,omitempty"`
	Subject string `json:"subject"`
	Says    string `json:"says"`
	Why     string `json:"why"`
	Answer  string `json:"answer" enum:"always|no|"`
	At      string `json:"at,omitempty"`
	// Declared is false for an answer to a right no manifest names: a command of deck or a site the browser acts on.
	Declared bool `json:"declared"`
	// Exit says the right is a way out of a turn: the network, money, sending in the owner's name, other modules.
	Exit bool `json:"exit"`
}

// rows are the rights m declares, a row a subject, with the owner's answers rows gives; then the answers to rights no
// manifest names.
func rows(p db.Project, m *manifest.Manifest, rows []db.RightGrant, tr func(string) string) []rightView {
	answer := func(right, who, subject string) (db.RightGrant, bool) {
		i := slices.IndexFunc(rows, func(r db.RightGrant) bool { return r.Name == right && r.Who == who && r.Subject == subject })
		if i < 0 {
			return db.RightGrant{}, false
		}
		return rows[i], true
	}
	var out []rightView
	seen := map[[3]string]bool{}
	for _, d := range m.Rights {
		r, _ := manifest.RightOf(d.Right)
		for _, subject := range d.What {
			key := [3]string{d.Right, d.Whom(), subject}
			if seen[key] {
				continue
			}
			seen[key] = true
			v := rightView{Project: p.Name, Right: d.Right, Who: d.Whom(), Subject: subject, Why: d.Why, Declared: true, Exit: r.Exit || d.Whom() == manifest.Agents,
				Says: manifest.Says(tr, d.Right, d.Who, []string{subject})}
			if g, ok := answer(d.Right, d.Whom(), subject); ok {
				v.Answer, v.At = g.Answer, g.CreatedAt
			}
			out = append(out, v)
		}
	}
	for _, g := range rows {
		if seen[[3]string{g.Name, g.Who, g.Subject}] {
			continue
		}
		r, _ := manifest.RightOf(g.Name)
		out = append(out, rightView{Project: p.Name, Right: g.Name, Who: g.Who, Subject: g.Subject, Answer: g.Answer, At: g.CreatedAt, Exit: r.Exit,
			Says: manifest.Says(tr, g.Name, g.Who, []string{g.Subject})})
	}
	return out
}

type rightsListIn struct {
	Project string `type:"project" opt:"" desc:"Project name; every module of the workspace when left out"`
}

// rightsList is what each module declares it may ask for and the owner's answers.
func (s *Service) rightsList(ctx context.Context, act registry.Actor, in rightsListIn) ([]rightView, error) {
	var ps []db.Project
	if in.Project != "" || act.Confined() {
		p, err := access.Project(ctx, s.St.Q, act, in.Project)
		if err != nil {
			return nil, err
		}
		ps = []db.Project{p}
	} else {
		all, err := s.St.Q.ListWorkspaceProjects(ctx, act.Workspace)
		if err != nil {
			return nil, err
		}
		ps = all
	}
	ws := words.In(registry.Lang(ctx))
	tr := func(t string) string { return manifest.Tr(ws, t) }
	out := []rightView{}
	for _, p := range ps {
		m, err := docs.Manifest(ctx, s.St.Q, p)
		if err != nil {
			if in.Project != "" {
				return nil, registry.Invalid("the manifest of %s does not load: %v", p.Name, err)
			}
			continue
		}
		gs, err := s.St.Q.ListGrants(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, rows(p, m, gs, tr)...)
	}
	return out, nil
}

type rightSetIn struct {
	Project string `type:"project" desc:"Project name"`
	Right   string `desc:"The right of the catalog" enum:"network|login|data|files|money|send|modules"`
	Who     string `opt:"" desc:"Of network: programs, the default, or agent" enum:"programs|agent|"`
	Subject string `desc:"What the right is on, as the module declares it: a host, a project, a command, a folder"`
	Answer  string `desc:"always: the module uses it without asking; no: it does not, and does not ask; revoke: forget the answer, so it asks again" enum:"always|no|revoke"`
}

// rightSet keeps the owner's answer to a right of a module on one subject: always or no for one its manifest
// declares or that the owner answered before, revoke for any answer.
func (s *Service) rightSet(ctx context.Context, act registry.Actor, in rightSetIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	n := registry.Need{Right: in.Right, Who: manifest.WhomOf(in.Right, in.Who), Subject: strings.TrimSpace(in.Subject)}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return nil, registry.Invalid("the manifest of %s does not load: %v", p.Name, err)
	}
	gs, err := s.St.Q.ListGrants(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	answered := slices.ContainsFunc(gs, func(g db.RightGrant) bool { return g.Name == n.Right && g.Who == n.Who && g.Subject == n.Subject })
	declared := slices.ContainsFunc(m.Rights, func(d manifest.Declared) bool {
		return d.Right == n.Right && d.Whom() == n.Who && slices.Contains(d.What, n.Subject)
	})
	switch {
	case in.Answer == "revoke" && !answered:
		return map[string]any{"ok": true, "unchanged": true}, nil
	case in.Answer != "revoke" && !declared && !answered:
		return nil, registry.Invalid("%s does not declare the right %s on %s; a module asks only for what its manifest declares", p.Name, n.Right, n.Subject)
	}
	n.Print = cmp.Or(m.PrintOf(n), s.printOf(ctx, p, n))
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if in.Answer == "revoke" {
			_, err := q.DeleteGrant(ctx, db.DeleteGrantParams{ProjectID: p.ID, Name: n.Right, Who: n.Who, Subject: n.Subject})
			return err
		}
		return docs.Grant(ctx, q, act, p.ID, n, in.Answer, s.Now())
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// printOf is the print of a tool of an MCP server of p as deck listed it, which only its running command knows; empty
// for any other subject.
func (s *Service) printOf(ctx context.Context, p db.Project, n registry.Need) string {
	if s.Registry == nil || n.Right == manifest.Network {
		return ""
	}
	c, err := s.Registry.Lookup(ctx, p.WorkspaceID, p.Name+"."+n.Subject)
	if err != nil || c.ProjectID != p.ID || c.Gate.Needs == nil || c.Gate.Fingerprint == nil {
		return ""
	}
	got := s.Gateway.Need(ctx, registry.Actor{Kind: registry.Agent, Workspace: p.WorkspaceID, ProjectID: p.ID}, c,
		c.Gate.Fingerprint(ctx, p.WorkspaceID, p.ID, nil), nil)
	if got.Right != n.Right || got.Subject != n.Subject {
		return ""
	}
	return got.Print
}

// grantDeclared answers always to every right m declares but send, which asks the owner when first used, and but
// those the owner answered already: what enabling a module gives it.
func (s *Service) grantDeclared(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project, m *manifest.Manifest) error {
	gs, err := q.ListGrants(ctx, p.ID)
	if err != nil {
		return err
	}
	for _, d := range m.Rights {
		if d.Right == manifest.Send {
			continue
		}
		for _, subject := range d.What {
			n := registry.Need{Right: d.Right, Who: d.Whom(), Subject: subject}
			if slices.ContainsFunc(gs, func(g db.RightGrant) bool { return g.Name == n.Right && g.Who == n.Who && g.Subject == n.Subject }) {
				continue
			}
			n.Print = m.PrintOf(n)
			if err := docs.Grant(ctx, q, act, p.ID, n, manifest.Always, s.Now()); err != nil {
				return err
			}
		}
	}
	return nil
}

// ungranted are the rights m declares the owner has not answered, in deck's words: what the module asks for when it
// first needs it, or the owner grants in its settings.
func (s *Service) ungranted(ctx context.Context, p db.Project, m *manifest.Manifest) []string {
	gs, err := s.St.Q.ListGrants(ctx, p.ID)
	if err != nil {
		return nil
	}
	var out []string
	for _, v := range rows(p, m, gs, func(t string) string { return t }) {
		if v.Declared && v.Answer == "" {
			out = append(out, v.Says)
		}
	}
	return out
}

// lines are the rights of a module as lines of markdown for the owner, what they let it do and why, the answer of
// each beside it when given; those enabling it leaves to ask when first used come last.
func lines(vs []rightView, tr func(string) string) []string {
	var now, later []string
	for _, v := range vs {
		line := "- " + bullet(v.Says, v.Why)
		switch {
		case v.Answer == manifest.No:
			line += " — " + tr("you said no")
		case v.Right == manifest.Send && v.Answer == "":
			later = append(later, line+" — "+tr("asks you the first time"))
			continue
		}
		now = append(now, line)
	}
	return append(now, later...)
}

// checkData verifies what a manifest declares it reads of other modules: modules there are, their collections, never
// the master's data or memory, every project only for its models and runs.
func checkData(ctx context.Context, q *db.Queries, at db.Project, m *manifest.Manifest) error {
	if at.Builtin == 1 {
		return nil
	}
	workspace, name := at.WorkspaceID, at.Name
	for _, subject := range m.Others() {
		project, rest, _ := strings.Cut(subject, ":")
		project, document, _ := strings.Cut(project, "/")
		project, collection, _ := strings.Cut(project, ".")
		switch {
		case project == "*" && rest != "":
			continue
		case project == "*":
			return registry.Invalid(`rights: data on * is every module's data, which only the master reads; name the modules, or *:models and *:runs`)
		case project == name:
			return registry.Invalid("rights: data on %s: a module reads its own data without a right", subject)
		}
		p, err := q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: project})
		if err != nil {
			return registry.Invalid("rights: data on %s: no module %q", subject, project)
		}
		if p.Builtin == 1 {
			return registry.Invalid("rights: data on %s: module %s keeps the owner's memory and topics; no module reads it", subject, p.Name)
		}
		if strings.HasPrefix(document, docs.MemoryDir) {
			return registry.Invalid("rights: data on %s: the memory of %s is for its own agents and the master only", subject, project)
		}
		if collection == "" {
			continue
		}
		other, err := docs.Manifest(ctx, q, p)
		if err != nil {
			return registry.Invalid("rights: data on %s: module %q has an invalid manifest: %v", subject, project, err)
		}
		if other.Collection(collection) == nil {
			return registry.Invalid("rights: data on %s: module %q has no collection %q", subject, project, collection)
		}
	}
	return nil
}

// tellRights writes once in the feed of the master of a workspace what format 17 did to the rights of its modules.
func (s *Service) tellRights(ctx context.Context, workspace int64) {
	text, err := s.St.Q.GetMark(ctx, db.GetMarkParams{WorkspaceID: workspace, Name: store.Rights17})
	if err != nil {
		return
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		m, err := access.Master(ctx, q, workspace)
		if err != nil {
			return err
		}
		if err := store.AddDeckNote(ctx, q, store.DeckNote{CreatedAt: store.Time(s.Now()), ProjectID: m.ID, Text: text}); err != nil {
			return err
		}
		return q.DeleteMark(ctx, db.DeleteMarkParams{WorkspaceID: workspace, Name: store.Rights17})
	})
	if err != nil {
		slog.Error("tell the rights of format 17", "err", err)
	}
}

func (s *Service) rightsCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("rights_list", "Rights of modules",
			"Lists the rights of deck's catalog each module declares it may ask for, a row a subject: what it lets the module do in deck's words, why in the module's, and the owner's answer, always or no, or none while it asks when first used; then the answers to rights no manifest names, such as a command of deck.",
			registry.OwnerSide, s.rightsList).On(events.Projects, events.Documents),
		registry.Def("right_set", "Answer a right",
			"Keeps the owner's answer to a right of a module on one subject: always, so it uses it without asking; no, so it does not and does not ask; or revoke, so it asks again when it next needs it.",
			registry.OwnerOnly, s.rightSet),
	}
}
