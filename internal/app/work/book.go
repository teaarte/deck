package work

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/models"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/words"
)

// Price is how the price of a model reads in the owner's words: by subscription, free, what it costs by its unit, or
// unknown; an unpriced model of a local connection is free.
func Price(v modelView, tr func(string) string) string {
	usd := func(f float64) string { return "$" + strconv.FormatFloat(f, 'f', -1, 64) }
	switch {
	case v.Subscription:
		return tr("subscription")
	case v.Free:
		return tr("free")
	case v.CallUSD != nil:
		return fmt.Sprintf(tr("%s a call"), usd(*v.CallUSD))
	case v.InputUSD == nil && v.Local:
		return tr("free")
	case v.InputUSD == nil:
		return tr("unknown")
	case v.Unit == models.UnitChar:
		return fmt.Sprintf(tr("%s per 1M chars"), usd(*v.InputUSD))
	case v.Unit == models.UnitSecond:
		return fmt.Sprintf(tr("%s a minute"), usd(*v.InputUSD*60/1000000))
	}
	out := 0.0
	if v.OutputUSD != nil {
		out = *v.OutputUSD
	}
	return fmt.Sprintf(tr("%s / %s per 1M"), usd(*v.InputUSD), usd(out))
}

// Note says a model in short: its price, the speed of its last self-check and the tokens a request holds, each when
// known.
func Note(v modelView, tr func(string) string) string {
	var parts []string
	if p := Price(v, tr); p != tr("unknown") {
		parts = append(parts, p)
	}
	if v.Speed > 0 {
		parts = append(parts, fmt.Sprintf(tr("%.0f tok/s"), v.Speed))
	}
	if v.Context > 0 {
		parts = append(parts, fmt.Sprintf(tr("context %s"), thousands(v.Context)))
	}
	return strings.Join(parts, " · ")
}

// thousands is a count of tokens in short, such as 128K or 1M.
func thousands(n int64) string {
	switch {
	case n >= 1000000 && n%100000 == 0:
		return strconv.FormatFloat(float64(n)/1000000, 'f', -1, 64) + "M"
	case n >= 1000:
		return strconv.FormatInt((n+500)/1000, 10) + "K"
	}
	return strconv.FormatInt(n, 10)
}

// LogoOf are the words naming the model for its mark: its id at the provider (else its name) first, so the maker's
// mark wins, then its connection and agent for a model whose maker has none.
func LogoOf(v modelView) string {
	words := []string{cmp.Or(v.Model, v.Name)}
	for _, w := range []string{v.Connection, v.Agent} {
		if w != "" && !slices.Contains(words, w) {
			words = append(words, w)
		}
	}
	return strings.Join(words, " ")
}

// Roles are the kinds of work a model can take: the agent work for a model that runs agent turns, else its kind.
func Roles(v modelView) []string {
	if v.Agent != "" {
		return []string{RoleAgent, RoleWidget}
	}
	return []string{v.Kind}
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
		b.add(modelView{Name: m.Name, Connection: r.Connection.Name, Model: m.Model, Kind: m.Kind, Agent: m.Agent, InputUSD: nullable(m.InputUsd), OutputUSD: nullable(m.OutputUsd),
			CallUSD: nullable(m.CallUsd), Unit: m.Unit, Free: m.Free != 0, Local: r.Connection.Local != 0, Subscription: m.Subscription != 0, Context: m.Context})
	}
	base := make([]modelView, 0, len(b.order))
	for _, n := range b.order {
		base = append(base, b.models[n])
	}
	for _, v := range s.onNodes(ctx, workspace, base) {
		b.add(v)
	}
	for n, v := range b.models {
		s.lastCheck(ctx, workspace, &v)
		b.models[n] = v
	}
	return b
}

// onNodes are the agents of a workspace on its nodes, each as the agent of base it runs there, named name@node.
func (s *Service) onNodes(ctx context.Context, workspace int64, base []modelView) []modelView {
	all, _ := s.All(ctx, workspace)
	var out []modelView
	for _, pv := range all {
		name := pv.Name()
		i := strings.LastIndex(name, "@")
		if i <= 0 {
			continue
		}
		for _, b := range base {
			if b.Name == name[:i] {
				b.Name, b.Node, b.Builtin = name, name[i+1:], false
				out = append(out, b)
			}
		}
	}
	return out
}

func (b *Book) add(v modelView) {
	v.Provider = providerOf(v, b.tr)
	b.models[v.Name] = v
	b.order = append(b.order, v.Name)
	slices.SortStableFunc(b.order, func(x, y string) int { return byProvider(b.models[x], b.models[y]) })
}

// Groups give the group of a model of a workspace where models are listed, in a language, and the place of the
// group among them.
func (s *Service) Groups(ctx context.Context, workspace int64, lang string) func(model string) (string, int) {
	ws := words.In(lang)
	b := s.book(ctx, workspace, func(p string) string { return manifest.TrText(ws, p) })
	return func(model string) (string, int) {
		v, ok := b.models[model]
		if !ok {
			return "", len(b.order)
		}
		return v.Provider, slices.IndexFunc(b.order, func(n string) bool { return byProvider(b.models[n], v) == 0 })
	}
}

// providerOf is the group a model stands in where models are listed: the subscriptions, else its connection, else
// the provider deck was started with.
func providerOf(v modelView, tr func(string) string) string {
	if v.Subscription {
		return tr("subscriptions")
	}
	return cmp.Or(v.Connection, v.Name)
}

// byProvider orders models by their group, the subscriptions first, then the built-in providers and the connections.
func byProvider(a, b modelView) int {
	sub := func(v modelView) int {
		if v.Subscription {
			return 0
		}
		return 1
	}
	return cmp.Or(cmp.Compare(sub(a), sub(b)), cmp.Compare(cmp.Or(a.Connection, a.Name), cmp.Or(b.Connection, b.Name)))
}

// Grouped is work with its choices in the order of the book and the group of each beside them.
func (b *Book) Grouped(w provider.Work) provider.Work {
	at := func(name string) int {
		if i := slices.Index(b.order, name); i >= 0 {
			return i
		}
		return len(b.order)
	}
	if w.Model != "" {
		w.Logo = b.Logo(w.Model)
	}
	w.Choices = slices.Clone(w.Choices)
	slices.SortStableFunc(w.Choices, func(x, y string) int { return cmp.Compare(at(x), at(y)) })
	// A group says whether its models cost money: the paid ones of a provider stand after its free ones.
	first := map[string]int{}
	for _, c := range w.Choices {
		if _, ok := first[b.models[c].Provider]; !ok {
			first[b.models[c].Provider] = len(first)
		}
	}
	slices.SortStableFunc(w.Choices, func(x, y string) int {
		return cmp.Or(cmp.Compare(first[b.models[x].Provider], first[b.models[y].Provider]), cmp.Compare(costRank(b.models[x]), costRank(b.models[y])))
	})
	w.ChoiceGroups = make([]string, len(w.Choices))
	w.ChoiceNotes = make([]string, len(w.Choices))
	w.ChoiceLogos = make([]string, len(w.Choices))
	for i, c := range w.Choices {
		w.ChoiceNotes[i], w.ChoiceLogos[i] = Note(b.models[c], b.tr), LogoOf(b.models[c])
		w.ChoiceGroups[i] = b.models[c].Provider
		// The subscriptions say what they are by their group already.
		if r := costRank(b.models[c]); r != 1 {
			w.ChoiceGroups[i] += " · " + b.tr(costWord[r])
		}
	}
	return w
}

// costWord says what a model costs: free, on a subscription, or money a call.
var costWord = []string{"free", "subscription", "paid"}

func costRank(m modelView) int {
	switch {
	case m.Subscription:
		return 1
	case m.Free || m.Local && m.InputUSD == nil && m.OutputUSD == nil && m.CallUSD == nil:
		return 0
	}
	return 2
}

// Note says a model of the book in short, empty for one it lacks.
func (b *Book) Note(name string) string {
	v, ok := b.models[name]
	if !ok {
		return ""
	}
	return Note(v, b.tr)
}

// Logo are the words naming a model of the book for its mark, the name alone for one it lacks.
func (b *Book) Logo(name string) string {
	v, ok := b.models[name]
	if !ok {
		return name
	}
	return LogoOf(v)
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
	ws := words.In(lang)
	b := s.book(ctx, workspace, func(p string) string { return manifest.TrText(ws, p) })
	return func(projectID int64, command string) provider.Work { return s.commandWork(ctx, b, projectID, command) }
}

// Facts say each model of a workspace in short in a language, the words naming it for its mark and the group of its
// provider.
func (s *Service) Facts(ctx context.Context, workspace int64, lang string) func(model string) (note, logo, group string) {
	ws := words.In(lang)
	b := s.book(ctx, workspace, func(p string) string { return manifest.TrText(ws, p) })
	return func(model string) (string, string, string) {
		return b.Note(model), b.Logo(model), b.models[model].Provider
	}
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
		w.Why = s.why(ctx, p.WorkspaceID, w.Models, w.Model, b.tr)
		w.Price = b.Price(w.Model)
		return b.Grouped(w)
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

// Places are the parts of the work of the projects of a workspace, by model: project.command for a command, the
// project for the kinds of work of its scripts.
func (s *Service) Places(ctx context.Context, workspace int64) map[string]provider.Placed {
	out := map[string]provider.Placed{}
	add := func(w provider.Work, place string) {
		names := w.Models
		if w.Model != "" && !slices.Contains(names, w.Model) {
			names = append([]string{w.Model}, names...)
		}
		for _, name := range names {
			p := out[name]
			if name == w.Model {
				p.Now = append(p.Now, place)
			} else if !slices.Contains(p.Fallback, place) {
				p.Fallback = append(p.Fallback, place)
			}
			out[name] = p
		}
	}
	projects, _ := s.St.Q.ListWorkspaceProjects(ctx, workspace)
	b := s.book(ctx, workspace, func(p string) string { return p })
	for _, p := range projects {
		for _, w := range s.rolesWork(ctx, b, p.ID) {
			add(w, p.Name)
		}
		m, err := docs.For(ctx, s.St.Q, registry.Actor{}, p.ID)
		if err != nil {
			continue
		}
		for _, c := range m.Commands {
			if c.Executor.Type == "agent" || c.Executor.Type == "model" {
				if w := s.commandWork(ctx, b, p.ID, c.Name); w.Model != "" {
					add(w, p.Name+"."+c.Name)
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
		if s.Up(ctx, p.WorkspaceID, name) {
			w.Model = name
			break
		}
	}
	if w.Model == "" && len(w.Models) > 0 {
		w.Model = w.Models[0]
	}
	w.Why = s.why(ctx, p.WorkspaceID, w.Models, w.Model, b.tr)
	w.Price = b.Price(w.Model)
	return b.Grouped(w)
}
