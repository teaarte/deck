package work

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/access"
	"deck2/internal/docs"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/words"
)

type modelView struct {
	Name       string `json:"name"`
	Connection string `json:"connection"`
	Model      string `json:"model"`
	Kind       string `json:"kind" enum:"text|image|tts|stt|music|video|search"`
	Agent      string `json:"agent,omitempty"`
	// Options are the fields deck adds to every call of the model, as JSON.
	Options   string   `json:"options,omitempty"`
	InputUSD  *float64 `json:"input_usd,omitempty"`
	OutputUSD *float64 `json:"output_usd,omitempty"`
	CallUSD   *float64 `json:"call_usd,omitempty"`
	Unit      string   `json:"unit" enum:"token|char|second"`
	Free      bool     `json:"free"`
	// Local is a model of the owner's own hardware: without a price it is free.
	Local bool `json:"-"`
	// Subscription is a model paid by the owner's subscription: what a call would cost is an estimate, out of the money spent.
	Subscription bool `json:"subscription"`
	// TPM and Context are the tokens a minute and of one request the model takes, 0 when not known.
	TPM     int64 `json:"tpm,omitempty"`
	Context int64 `json:"context,omitempty"`
	// Builtin is a provider deck was started with; Status, Error, Verdict, Speed and Version are of its last self-check.
	Builtin bool `json:"builtin"`
	// Spare says the calling module may remove it without the owner: its own call added it, free, in no assignment.
	Spare   bool    `json:"spare,omitempty"`
	Status  string  `json:"status,omitempty" enum:"ok|warning|error|checking|paused|needs_login|missing|off"`
	Error   string  `json:"error,omitempty"`
	Verdict string  `json:"verdict,omitempty"`
	Speed   float64 `json:"speed,omitempty"`
	Version string  `json:"version,omitempty"`
	// Price is how its price reads; Used says some work of a project goes to it now or is assigned to it.
	Price string `json:"price"`
	Used  bool   `json:"used"`
	// Provider is the group it stands in: the subscriptions, else its connection.
	Provider string `json:"provider"`
	// Node is the node an agent runs on, such as claude-code@Mac on Mac; empty for one of deck's machine.
	Node string `json:"node,omitempty"`
	// Projects is how many modules some work of goes to it, deck's own defaults apart; Places that work, first or as a
	// fallback.
	Projects int      `json:"projects"`
	Places   []string `json:"places,omitempty"`
	// UsedIn says in words how many modules it is in, empty for none.
	UsedIn string `json:"used_in,omitempty"`
	// Roles are the kinds of work it can take; Note says it in short, such as price, speed and context; Logo are
	// the words naming the model itself, for its mark: its maker's, the provider's mark is that of Provider.
	Roles []string `json:"roles"`
	Note  string   `json:"note,omitempty"`
	Logo  string   `json:"logo"`
}

// lastCheck fills in the state of a model: of its last self-check in an agent, else of its last call.
func (s *Service) lastCheck(ctx context.Context, workspace int64, v *modelView) {
	if h, err := s.Health(ctx, workspace, v.Name); err == nil {
		v.Status, v.Error, v.Verdict, v.Speed, v.Version = h.Status, h.Error, h.Verdict, h.Speed, h.Version
	}
}

type modelsView struct {
	Agents      []agentView      `json:"agents"`
	Connections []connectionView `json:"connections"`
	Models      []modelView      `json:"models"`
	Assignments []assignmentView `json:"assignments"`
	// Choices are the models of deck that could do each kind of work, once a kind.
	Choices []choiceView `json:"choices"`
	// Uses are the kinds of work the projects call models for apart from their commands, such as the pictures of
	// their scripts, with the model each goes to.
	Uses []useView `json:"uses"`
	// Work is every command of a project that runs on a model and every kind of work of its scripts, with the model
	// each runs on; without a project, of every project and then the defaults of each kind of work.
	Work []workView `json:"work"`
	// Waits are what of the models waits for the owner, the broken first.
	Waits []waitView `json:"waits"`
}

// choiceView is the models of deck that could do a kind of work, in the order deck lists them, with the group of each.
type choiceView struct {
	Role   string   `json:"role" enum:"agent|text|image|tts|stt|music|video|search|widget"`
	Models []string `json:"models"`
	Groups []string `json:"groups"`
}

type useView struct {
	Project string `json:"project"`
	provider.Work
}

// workView is a command or a kind of work of scripts of a project with its model; Own says the project or the
// command has an assignment of its own, else the model is the default.
type workView struct {
	// Kind is a command, the work of scripts or the default of a kind of work where nothing has its own.
	Kind         string `json:"kind" enum:"command|script|default"`
	Project      string `json:"project"`
	ProjectTitle string `json:"project_title,omitempty"`
	Command      string `json:"command,omitempty"`
	Title        string `json:"title"`
	Own          bool   `json:"own"`
	provider.Work
}

// roleWord names a kind of work so that agent work and one answer of a text model read apart.
func roleWord(role string) string {
	switch role {
	case RoleAgent:
		return "agent, with tools"
	case RoleText:
		return "text, one answer"
	}
	return role
}

func nullable(f sql.NullFloat64) *float64 {
	if !f.Valid {
		return nil
	}
	return &f.Float64
}

type modelsListIn struct {
	Project string `type:"project" opt:"" desc:"Only the assignments and uses of this project and its commands"`
	Lang    string `opt:"" desc:"Language of the prices, such as ru; English without one"`
}

func (s *Service) modelsList(ctx context.Context, act registry.Actor, in modelsListIn) (modelsView, error) {
	ws := words.In(registry.Lang(ctx))
	tr := func(p string) string { return manifest.TrText(ws, p) }
	out := modelsView{Agents: s.agents(ctx, act.Workspace, tr), Connections: []connectionView{}, Models: []modelView{}, Assignments: []assignmentView{},
		Choices: []choiceView{}, Uses: []useView{}, Work: []workView{}}
	conns, err := s.St.Q.ListConnections(ctx, act.Workspace)
	if err != nil {
		return out, err
	}
	models, err := s.St.Q.ListModels(ctx, act.Workspace)
	if err != nil {
		return out, err
	}
	for _, c := range conns {
		v := connectionView{Name: c.Name, API: c.Api, URL: c.Url, Key: "none", Local: c.Local != 0, Online: true}
		if c.NodeID.Valid {
			v.Online = s.Online(c.NodeID.Int64)
			if d, err := s.St.Q.NodeByID(ctx, c.NodeID.Int64); err == nil {
				v.Node = d.Name
			}
		}
		if strings.ContainsAny(c.Url, "@?#") {
			v.URL = "(an address with a key in it; set it again without)"
		}
		if s.Key(act.Workspace, c.Name) != "" {
			v.Key = "set"
		}
		switch {
		case v.Node != "" && v.Online:
			v.Where = tr(fmt.Sprintf("through node %s", v.Node))
		case v.Node != "":
			v.Where = tr(fmt.Sprintf("through node %s, away now", v.Node))
		case c.Api == "cli":
			v.Where = tr("the CLI on the subscription")
		default:
			v.Where = tr("deck reaches it itself")
		}
		for _, m := range models {
			if m.Model.ConnectionID == c.ID {
				v.Models++
			}
		}
		out.Connections = append(out.Connections, v)
	}
	for _, pv := range s.Builtin {
		_, sub := pv.(provider.Subscribed)
		v := modelView{Name: pv.Name(), Kind: RoleText, Agent: pv.Name(), Builtin: true, Subscription: sub}
		s.lastCheck(ctx, act.Workspace, &v)
		s.offStatus(ctx, act.Workspace, &v)
		out.Models = append(out.Models, v)
	}
	for _, r := range models {
		m := r.Model
		v := modelView{Name: m.Name, Connection: r.Connection.Name, Model: m.Model, Kind: m.Kind, Agent: m.Agent, Options: s.shownOptions(ctx, m.Options),
			InputUSD: nullable(m.InputUsd), OutputUSD: nullable(m.OutputUsd), CallUSD: nullable(m.CallUsd), Unit: m.Unit, Free: m.Free != 0, Local: r.Connection.Local != 0,
			Subscription: m.Subscription != 0, TPM: m.Tpm, Context: m.Context}
		if act.Confined() {
			v.Spare = s.Spare(ctx, act, m)
		}
		s.lastCheck(ctx, act.Workspace, &v)
		s.offStatus(ctx, act.Workspace, &v)
		out.Models = append(out.Models, v)
	}
	for _, v := range s.onNodes(ctx, act.Workspace, out.Models) {
		s.lastCheck(ctx, act.Workspace, &v)
		out.Models = append(out.Models, v)
	}
	for i := range out.Models {
		m := &out.Models[i]
		m.Provider, m.Roles, m.Note, m.Logo = providerOf(*m, tr), Roles(*m), Note(*m, tr), LogoOf(*m)
	}
	rows, err := s.St.Q.ListAssignments(ctx, act.Workspace)
	if err != nil {
		return out, err
	}
	var pid int64
	own := ""
	if act.Confined() {
		p, err := access.ReadModels(ctx, s.St.Q, act, in.Project)
		if err != nil {
			return out, err
		}
		pid, own = p.ID, p.Name
	} else if pid, err = access.Filter(ctx, s.St.Q, act, in.Project); err != nil {
		return out, err
	}
	for _, r := range rows {
		if in.Project != "" && r.Project != in.Project {
			continue
		}
		if act.Confined() && r.Project != "" && r.Project != own {
			continue
		}
		v := assignmentView{Project: r.Project, Command: r.Command, Role: r.Role}
		_ = json.Unmarshal([]byte(r.Models), &v.Models)
		for _, name := range v.Models {
			if s.Exists(ctx, act.Workspace, name) && s.Up(ctx, act.Workspace, name) {
				v.Using = name
				break
			}
		}
		v.Why = s.why(ctx, act.Workspace, v.Models, v.Using, tr)
		out.Assignments = append(out.Assignments, v)
	}
	used, err := s.uses(ctx, act, pid, registry.Lang(ctx), &out)
	if err != nil {
		return out, err
	}
	b := s.book(ctx, act.Workspace, tr)
	for _, role := range roles {
		w := provider.Work{Choices: b.Of(role)}
		if agentic(role) {
			w.Choices = b.Agents()
		}
		w = b.Grouped(w)
		out.Choices = append(out.Choices, choiceView{Role: role, Models: w.Choices, Groups: w.ChoiceGroups})
	}
	places := placesOf(out.Work, tr)
	for i := range out.Models {
		out.Models[i].Price, out.Models[i].Used = Price(out.Models[i], tr), used[out.Models[i].Name]
		out.Models[i].Error = tr(out.Models[i].Error)
		p := places[out.Models[i].Name]
		n := len(p.projects)
		if p.projects[""] {
			n--
		}
		out.Models[i].Projects, out.Models[i].Places = n, p.places
		switch {
		case n == 1:
			out.Models[i].UsedIn = tr("in one module")
		case n > 1:
			out.Models[i].UsedIn = tr(fmt.Sprintf("in %d modules", n))
		case p.projects[""]:
			out.Models[i].UsedIn = tr("default")
		}
	}
	// By provider, and in each those that work goes to first.
	slices.SortStableFunc(out.Models, func(a, b modelView) int {
		return cmp.Or(byProvider(a, b), cmp.Compare(boolRank(b.Used), boolRank(a.Used)))
	})
	for i := range out.Agents {
		out.Agents[i].Error = tr(out.Agents[i].Error)
	}
	out.Waits = waits(&out, tr)
	return out, nil
}

type placed struct {
	projects map[string]bool
	places   []string
}

// placesOf are, by model, the modules and the work that goes to it, first or as a fallback.
func placesOf(work []workView, tr func(string) string) map[string]placed {
	out := map[string]placed{}
	for _, w := range work {
		names := w.Models
		if len(names) == 0 && w.Model != "" {
			names = []string{w.Model}
		}
		for i, name := range names {
			p := out[name]
			if p.projects == nil {
				p.projects = map[string]bool{}
			}
			p.projects[w.Project] = true
			place := cmp.Or(w.ProjectTitle, "deck") + " · " + w.Title
			if i > 0 {
				place += " (" + tr("fallback") + ")"
			}
			if !slices.Contains(p.places, place) {
				p.places = append(p.places, place)
			}
			out[name] = p
		}
	}
	return out
}

// uses fills in the uses of the projects the caller reaches, all of them or only pid, with the work of pid, and says
// which models some work goes to or is assigned to.
func (s *Service) uses(ctx context.Context, act registry.Actor, pid int64, lang string, out *modelsView) (map[string]bool, error) {
	used := map[string]bool{}
	for _, a := range out.Assignments {
		for _, name := range a.Models {
			used[name] = true
		}
	}
	projects, err := s.St.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	ws := words.In(lang)
	b := s.book(ctx, act.Workspace, func(p string) string { return manifest.TrText(ws, p) })
	for _, p := range projects {
		if pid != 0 && p.ID != pid {
			continue
		}
		roles := s.rolesWork(ctx, b, p.ID)
		for _, w := range roles {
			out.Uses = append(out.Uses, useView{Project: p.Name, Work: w})
		}
		m, err := docs.For(ctx, s.St.Q, registry.Actor{}, p.ID)
		if err != nil {
			continue
		}
		mws := manifest.Words(ws, m, lang)
		title, _, _ := manifest.Nav(p.Name, m, mws)
		own := func(command, role string) bool {
			return slices.ContainsFunc(out.Assignments, func(a assignmentView) bool {
				return a.Project == p.Name && a.Role == role && (a.Command == command || a.Command == "") && len(a.Models) > 0
			})
		}
		for _, c := range m.Commands {
			if c.Executor.Type == "agent" || c.Executor.Type == "model" {
				w := s.commandWork(ctx, b, p.ID, c.Name)
				out.Work = append(out.Work, workView{Kind: "command", Project: p.Name, ProjectTitle: title, Command: c.Name,
					Title: manifest.Tr(mws, c.Title), Own: own(c.Name, w.Role), Work: w})
			}
		}
		for _, w := range roles {
			out.Work = append(out.Work, workView{Kind: "script", Project: p.Name, ProjectTitle: title, Title: manifest.Tr(ws, roleWord(w.Role)),
				Own: own("", w.Role), Work: w})
		}
	}
	if pid == 0 {
		for _, a := range out.Assignments {
			if a.Project != "" {
				continue
			}
			w := provider.Work{Role: a.Role, Models: a.Models, Model: a.Using, Why: a.Why}
			if len(a.Models) > 0 {
				w.Model, w.Price = a.Models[0], b.Price(a.Models[0])
			}
			if w.Choices = b.Of(a.Role); agentic(a.Role) {
				w.Choices = b.Agents()
			}
			out.Work = append(out.Work, workView{Kind: "default", Title: manifest.Tr(ws, roleWord(a.Role)), Own: true, Work: b.Grouped(w)})
		}
		// The views of cards go to the master's model until the owner picks one.
		if !slices.ContainsFunc(out.Assignments, func(a assignmentView) bool { return a.Project == "" && a.Role == RoleWidget }) {
			out.Work = append(out.Work, workView{Kind: "default", Title: manifest.Tr(ws, RoleWidget), Work: b.Grouped(provider.Work{Role: RoleWidget, Choices: b.Agents()})})
		}
	}
	for name := range s.Places(ctx, act.Workspace) {
		used[name] = true
	}
	return used, nil
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// offStatus marks a model whose program is not installed or that the owner turned off, in place of its last check.
func (s *Service) offStatus(ctx context.Context, workspace int64, v *modelView) {
	if off := s.Off(ctx, workspace, v.Name); off != "" {
		v.Status, v.Error, v.Verdict, v.Speed = off, "", "", 0
	}
}

type connectionView struct {
	Name string `json:"name"`
	API  string `json:"api" enum:"openai|google|anthropic|raw|cli"`
	URL  string `json:"url"`
	// Key says whether deck keeps an API key of the connection, apart from the secrets of modules.
	Key    string `json:"key" enum:"set|none"`
	Local  bool   `json:"local"`
	Models int64  `json:"models"`
	// Node is the node whose machine serves the address, Online whether it is connected now.
	Node   string `json:"node,omitempty"`
	Online bool   `json:"online"`
	// Where says in words where its models run: deck's machine, or a node and whether it is away.
	Where string `json:"where"`
}

// agentView is a program models run in: a built-in provider or an ACP agent, with its version and health; an ACP
// agent that is not installed is missing.
type agentView struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Status  string `json:"status" enum:"ok|warning|error|checking|paused|needs_login|missing|off"`
	Error   string `json:"error"`
	// Builtin is a provider deck was started with, which the owner may turn off.
	Builtin bool `json:"builtin"`
}

// agents are the built-in providers with a self-check and the ACP agents deck knows; a built-in one that is not
// installed says how to install it, one the workspace turned off says so.
func (s *Service) agents(ctx context.Context, workspace int64, tr func(string) string) []agentView {
	var out []agentView
	for _, pv := range s.Builtin {
		if _, ok := pv.(provider.SelfChecker); !ok {
			continue
		}
		v := agentView{Name: pv.Name(), Builtin: true}
		if h, err := health.Get(ctx, s.St.Q, health.Of{}, health.Provider(pv.Name())); err == nil {
			v.Version, v.Status, v.Error = h.Version, h.Status, h.Error
		}
		if off := s.Off(ctx, workspace, pv.Name()); off != "" {
			v.Status, v.Error = off, ""
		}
		if i, ok := pv.(provider.Installed); ok && v.Status == health.Missing {
			v.Error = tr(fmt.Sprintf("not installed; install it: %s", i.Install()))
		}
		out = append(out, v)
	}
	for _, name := range acp.Names() {
		v := agentView{Name: name, Status: health.Missing}
		if s.Installed(name) {
			v.Status = "checking"
		}
		if h, err := health.Get(ctx, s.St.Q, health.Of{}, health.Agent(name)); err == nil {
			v.Version, v.Status, v.Error = h.Version, h.Status, h.Error
		}
		out = append(out, v)
	}
	return out
}

// ModelView is a model as its form shows it, a file of its options by its name and size.
func (s *Service) ModelView(ctx context.Context, m db.Model, connection string) any {
	return modelView{Name: m.Name, Connection: connection, Model: m.Model, Kind: m.Kind, Agent: m.Agent, Options: s.shownOptions(ctx, m.Options),
		InputUSD: nullable(m.InputUsd), OutputUSD: nullable(m.OutputUsd), CallUSD: nullable(m.CallUsd), Unit: m.Unit, Free: m.Free != 0,
		Subscription: m.Subscription != 0, TPM: m.Tpm, Context: m.Context}
}

func (s *Service) listCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("models_list", "Models",
			"Lists the connections models are reached through, the models with their kind and price, the models that could do each kind of work, and which models do which work.",
			registry.Everyone, s.modelsList).On("models", "assignments", "projects", "health", "settings"),
	}
}
