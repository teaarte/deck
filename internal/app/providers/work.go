package providers

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// Price is how the price of a model reads in the owner's words: by subscription, free, or what it costs by its unit.
func Price(v modelView, tr func(string) string) string {
	usd := func(f float64) string { return "$" + strconv.FormatFloat(f, 'f', -1, 64) }
	switch {
	case v.Subscription:
		return tr("subscription")
	case v.Free:
		return tr("free")
	case v.CallUSD != nil:
		return fmt.Sprintf(tr("%s a call"), usd(*v.CallUSD))
	case v.InputUSD == nil:
		return ""
	case v.Unit == UnitChar:
		return fmt.Sprintf(tr("%s per 1M chars"), usd(*v.InputUSD))
	case v.Unit == UnitSecond:
		return fmt.Sprintf(tr("%s a minute"), usd(*v.InputUSD*60/1000000))
	}
	out := 0.0
	if v.OutputUSD != nil {
		out = *v.OutputUSD
	}
	return fmt.Sprintf(tr("%s / %s per 1M"), usd(*v.InputUSD), usd(out))
}

// Book is the models of a workspace by name, with what reads their price: the built-in providers and those of
// connections.
type Book struct {
	models map[string]modelView
	order  []string
	tr     func(string) string
}

// book reads the models of a workspace once, for many lookups of the work of commands.
func (s *Service) book(ctx context.Context, workspace int64, tr func(string) string) *Book {
	b := &Book{models: map[string]modelView{}, tr: tr}
	for _, pv := range s.Builtin {
		_, sub := pv.(provider.Subscribed)
		b.add(modelView{Name: pv.Name(), Kind: RoleText, Agent: pv.Name(), Builtin: true, Subscription: sub})
	}
	rows, _ := s.St.Q.ListModels(ctx, workspace)
	for _, r := range rows {
		m := r.Model
		b.add(modelView{Name: m.Name, Kind: m.Kind, Agent: m.Agent, InputUSD: nullable(m.InputUsd), OutputUSD: nullable(m.OutputUsd),
			CallUSD: nullable(m.CallUsd), Unit: m.Unit, Free: m.Free != 0, Subscription: m.Subscription != 0})
	}
	return b
}

func (b *Book) add(v modelView) {
	b.models[v.Name] = v
	b.order = append(b.order, v.Name)
}

// Price is the price of a model of the book, empty for one it lacks.
func (b *Book) Price(name string) string {
	v, ok := b.models[name]
	if !ok {
		return ""
	}
	return Price(v, b.tr)
}

// Of are the models of the book of a kind of work, in the order deck lists them.
func (b *Book) Of(kind string) []string {
	var out []string
	for _, name := range b.order {
		if b.models[name].Kind == kind && b.models[name].Agent == "" {
			out = append(out, name)
		}
	}
	return out
}

// Works looks up the work of commands of a workspace on one book of its models, its prices in a language.
func (s *Service) Works(ctx context.Context, workspace int64, lang string) func(projectID int64, command string) provider.Work {
	ws := s.Words(lang)
	b := s.book(ctx, workspace, func(p string) string { return manifest.Tr(ws, p) })
	return func(projectID int64, command string) provider.Work { return s.commandWork(ctx, b, projectID, command) }
}

// Agents are the models of the book that run agent turns: the built-in providers and the models in an agent.
func (b *Book) Agents() []string {
	var out []string
	for _, name := range b.order {
		if b.models[name].Agent != "" {
			out = append(out, name)
		}
	}
	return out
}

// commandWork is who does the work of a command of a project by the manifest in force: an agent command's model,
// a model command's model of its kind; nothing for the other commands.
func (s *Service) commandWork(ctx context.Context, b *Book, projectID int64, command string) provider.Work {
	m, err := docs.For(ctx, s.St.Q, registry.Actor{}, projectID)
	if err != nil {
		return provider.Work{}
	}
	cmd := m.Command(command)
	if cmd == nil {
		return provider.Work{}
	}
	p, err := s.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return provider.Work{}
	}
	switch cmd.Executor.Type {
	case "agent":
		w := provider.Work{Role: RoleAgent, Choices: s.Able(ctx, p.WorkspaceID, cmd)}
		w.Models, _ = Assigned(ctx, s.St.Q, p.WorkspaceID, p.ID, command, RoleAgent)
		if pv, err := s.For(ctx, projectID, cmd); err == nil {
			w.Model = pv.Name()
		}
		w.Price = b.Price(w.Model)
		return w
	case "model":
		role, ok := strings.CutPrefix(cmd.Executor.Model, "@")
		if cmd.Executor.Model == "" {
			role, ok = cmd.Executor.Kind, true
		}
		if !ok {
			return provider.Work{Role: cmd.Executor.Kind, Model: cmd.Executor.Model, Price: b.Price(cmd.Executor.Model)}
		}
		return s.roleWork(ctx, b, p, command, role)
	}
	return provider.Work{}
}

// Places are the parts of the work of the projects of a workspace each model does now, by model: project.command for a
// command, the project for the kinds of work of its scripts.
func (s *Service) Places(ctx context.Context, workspace int64) map[string][]string {
	out := map[string][]string{}
	projects, _ := s.St.Q.ListWorkspaceProjects(ctx, workspace)
	b := s.book(ctx, workspace, func(p string) string { return p })
	for _, p := range projects {
		for _, w := range s.rolesWork(ctx, b, p.ID) {
			out[w.Model] = append(out[w.Model], p.Name)
		}
		m, err := docs.For(ctx, s.St.Q, registry.Actor{}, p.ID)
		if err != nil {
			continue
		}
		for _, c := range m.Commands {
			if c.Executor.Type == "agent" || c.Executor.Type == "model" {
				if w := s.commandWork(ctx, b, p.ID, c.Name); w.Model != "" {
					out[w.Model] = append(out[w.Model], p.Name+"."+c.Name)
				}
			}
		}
	}
	return out
}

// rolesWork is who does each kind of work a project calls models for apart from its commands, such as the pictures
// its scripts make, by the @roles of its manifest in force.
func (s *Service) rolesWork(ctx context.Context, b *Book, projectID int64) []provider.Work {
	m, err := docs.For(ctx, s.St.Q, registry.Actor{}, projectID)
	if err != nil {
		return nil
	}
	p, err := s.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return nil
	}
	var out []provider.Work
	for _, use := range m.Models {
		if role, ok := strings.CutPrefix(use, "@"); ok && slices.Contains(roles, role) {
			out = append(out, s.roleWork(ctx, b, p, "", role))
		}
	}
	return out
}

func (s *Service) roleWork(ctx context.Context, b *Book, p db.Project, command, role string) provider.Work {
	w := provider.Work{Role: role, Choices: append([]string{}, b.Of(role)...)}
	w.Models, _ = Assigned(ctx, s.St.Q, p.WorkspaceID, p.ID, command, role)
	for _, name := range w.Models {
		if s.Up(ctx, p.WorkspaceID, name, true) {
			w.Model = name
			break
		}
	}
	if w.Model == "" && len(w.Models) > 0 {
		w.Model = w.Models[0]
	}
	w.Price = b.Price(w.Model)
	return w
}
