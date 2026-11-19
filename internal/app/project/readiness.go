package project

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"deck2/internal/access"
	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/words"
)

// needRow is one thing a module needs to work: a right enabling grants, a secret, a program or a model.
type needView struct {
	Kind   string `json:"kind" enum:"right|secret|program|model"`
	What   string `json:"what"`
	Detail string `json:"detail"`
	Done   bool   `json:"done"`
	// Optional is a connection or a model it can call besides its roles, which it works without.
	Optional bool `json:"optional,omitempty"`
}

// projectNeeds sums up what a module needs to work: the rights enabling grants, the secrets it names, the programs
// its scripts miss with how to install them, and its work no model takes.
type projectReadinessGetIn struct {
	Project string `type:"project" desc:"Project name"`
	Lang    string `opt:"" desc:"Language of the words, such as ru; English without one"`
}

func (s *Service) projectNeeds(ctx context.Context, act registry.Actor, in projectReadinessGetIn) ([]needView, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	ms, failed := docs.All(ctx, s.St.Q, []db.Project{p})
	if err := failed[p.ID]; err != nil {
		return nil, registry.Invalid("the manifest of %s does not load: %v", p.Name, err)
	}
	gs, err := s.St.Q.ListGrants(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	ws := words.In(registry.Lang(ctx))
	return s.needs(ctx, p, ms[p.ID], docs.RightRows(p, ms[p.ID], gs, func(t string) string { return manifest.Tr(ws, t) }, nil), ws), nil
}

func (s *Service) needs(ctx context.Context, p db.Project, m *manifest.Manifest, rights []docs.RightRow, ws map[string]string) []needView {
	out := []needView{}
	for _, r := range rights {
		if r.Declared {
			out = append(out, needView{Kind: "right", What: r.Says, Detail: r.Why, Done: r.Answer == manifest.Always})
		}
	}
	if m == nil {
		return out
	}
	for _, str := range m.Secrets() {
		out = append(out, needView{Kind: "secret", What: str, Done: s.Secret(p.WorkspaceID, p.Name, str)})
	}
	missing := s.Missing(ctx, p.ID, m)
	for _, name := range slices.Sorted(maps.Keys(missing)) {
		out = append(out, needView{Kind: "program", What: name, Detail: manifest.TrText(ws, missing[name].Error())})
	}
	out = append(out, s.missingModels(ctx, p.WorkspaceID, m, ws)...)
	work := s.Unassigned(ctx, p.WorkspaceID, p.ID, m)
	for _, name := range slices.Sorted(maps.Keys(work)) {
		out = append(out, needView{Kind: "model", What: name, Detail: manifest.TrText(ws, work[name])})
	}
	return out
}

func (s *Service) readinessCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("project_readiness_get", "What a module needs",
			"Sums up what a module needs to work: the rights enabling it grants, the secrets it names and whether they are set, the programs its scripts miss with how to install them, and its work no model of deck takes.",
			registry.OwnerSide, s.projectNeeds).On(events.Projects, events.Documents, events.Secrets, events.Models, events.Assignments),
	}
}

// missingModels are the connections, models and @roles a module calls models through that deck lacks: a role it needs,
// a connection or a model it works without, as another owner has their own.
func (s *Service) missingModels(ctx context.Context, workspace int64, m *manifest.Manifest, ws map[string]string) []needView {
	have := map[string]bool{}
	if cs, err := s.St.Q.ListConnections(ctx, workspace); err == nil {
		for _, c := range cs {
			have[c.Name] = true
		}
	}
	if ms, err := s.St.Q.ListModels(ctx, workspace); err == nil {
		for _, x := range ms {
			have[x.Model.Name] = true
		}
	}
	if as, err := s.St.Q.ListAssignments(ctx, workspace); err == nil {
		for _, a := range as {
			have["@"+a.Role] = a.Models != "" && a.Models != "[]"
		}
	}
	var out []needView
	for _, u := range m.Called() {
		conn, _, _ := strings.Cut(u, "/")
		switch {
		case have[u] || have[conn] && !strings.HasPrefix(u, "@"):
		case strings.HasPrefix(u, "@"):
			out = append(out, needView{Kind: "model", What: u, Detail: manifest.Tr(ws, "Assign a model to the role in Settings → Models.")})
		default:
			out = append(out, needView{Kind: "model", What: u, Detail: fmt.Sprintf(cmp.Or(ws[addConnection], addConnection), conn), Optional: true})
		}
	}
	return out
}

const addConnection = "Optional: add the connection %s in Settings → Models → Connections, or a model of that name, and it calls its models too."
