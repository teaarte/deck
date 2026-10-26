package providers

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
)

// Units are what a price per million of a model counts: tokens in and out, characters deck sent, or seconds of a
// recording the API reported.
const (
	UnitToken  = "token"
	UnitChar   = "char"
	UnitSecond = "second"
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
	Builtin bool    `json:"builtin"`
	Status  string  `json:"status,omitempty" enum:"ok|warning|error|checking|paused|needs_login|missing"`
	Error   string  `json:"error,omitempty"`
	Verdict string  `json:"verdict,omitempty"`
	Speed   float64 `json:"speed,omitempty"`
	Version string  `json:"version,omitempty"`
	// Price is how its price reads; Used says some work of a project goes to it now or is assigned to it.
	Price string `json:"price"`
	Used  bool   `json:"used"`
	// Provider is the group it stands in: the subscriptions, else its connection.
	Provider string `json:"provider"`
}

// lastCheck fills in the state of a model: of its last self-check in an agent, else of its last call.
func (s *Service) lastCheck(ctx context.Context, workspace int64, v *modelView) {
	if h, err := s.healthOf(ctx, workspace, v.Name); err == nil {
		v.Status, v.Error, v.Verdict, v.Speed, v.Version = h.Status, h.Error, h.Verdict, h.Speed, h.Version
	}
}

type modelsOut struct {
	Agents      []agentView      `json:"agents"`
	Connections []connectionView `json:"connections"`
	Models      []modelView      `json:"models"`
	Assignments []assignmentView `json:"assignments"`
	// Uses are the kinds of work the projects call models for apart from their commands, such as the pictures of
	// their scripts, with the model each goes to.
	Uses []useView `json:"uses"`
	// Work is every command of a project that runs on a model and every kind of work of its scripts, with the model
	// each runs on; without a project, of every project and then the defaults of each kind of work.
	Work []workView `json:"work"`
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

func (s *Service) modelsList(ctx context.Context, act registry.Actor, in modelsListIn) (modelsOut, error) {
	out := modelsOut{Agents: s.agents(ctx), Connections: []connectionView{}, Models: []modelView{}, Assignments: []assignmentView{}, Uses: []useView{}, Work: []workView{}}
	conns, err := s.St.Q.ListConnections(ctx, act.Workspace)
	if err != nil {
		return out, err
	}
	models, err := s.St.Q.ListModels(ctx, act.Workspace)
	if err != nil {
		return out, err
	}
	for _, c := range conns {
		v := connectionView{Name: c.Name, API: c.Api, URL: c.Url, Key: "none", Local: c.Local != 0}
		if strings.ContainsAny(c.Url, "@?#") {
			v.URL = "(an address with a key in it; set it again without)"
		}
		switch {
		case c.KeySecret != "" && act.OwnerSide():
			v.Key, v.KeySecret = "secret", c.KeySecret
		case c.KeySecret != "":
			v.Key = "secret"
		}
		for _, m := range models {
			if m.Model.ConnectionID == c.ID {
				v.Models++
			}
		}
		out.Connections = append(out.Connections, v)
	}
	ws := s.Words(in.Lang)
	tr := func(p string) string { return manifest.TrText(ws, p) }
	for _, pv := range s.Builtin {
		_, sub := pv.(provider.Subscribed)
		v := modelView{Name: pv.Name(), Kind: RoleText, Agent: pv.Name(), Builtin: true, Subscription: sub}
		s.lastCheck(ctx, act.Workspace, &v)
		out.Models = append(out.Models, v)
	}
	for _, r := range models {
		m := r.Model
		v := modelView{Name: m.Name, Connection: r.Connection.Name, Model: m.Model, Kind: m.Kind, Agent: m.Agent, Options: s.shownOptions(ctx, m.Options),
			InputUSD: nullable(m.InputUsd), OutputUSD: nullable(m.OutputUsd), CallUSD: nullable(m.CallUsd), Unit: m.Unit, Free: m.Free != 0, Local: r.Connection.Local != 0,
			Subscription: m.Subscription != 0, TPM: m.Tpm, Context: m.Context}
		s.lastCheck(ctx, act.Workspace, &v)
		out.Models = append(out.Models, v)
	}
	for i := range out.Models {
		out.Models[i].Provider = providerOf(out.Models[i], tr)
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
			if s.exists(ctx, act.Workspace, name) && s.Up(ctx, act.Workspace, name) {
				v.Using = name
				break
			}
		}
		v.Why = s.why(ctx, act.Workspace, v.Models, v.Using, tr)
		out.Assignments = append(out.Assignments, v)
	}
	used, err := s.uses(ctx, act, pid, in.Lang, &out)
	if err != nil {
		return out, err
	}
	b := s.book(ctx, act.Workspace, tr)
	for i, a := range out.Assignments {
		out.Assignments[i].Choices = b.Of(a.Role)
		if agentic(a.Role) {
			out.Assignments[i].Choices = b.Agents()
		}
		if len(a.Models) > 0 {
			out.Assignments[i].Price = b.Price(a.Models[0])
		}
	}
	for i := range out.Models {
		out.Models[i].Price, out.Models[i].Used = Price(out.Models[i], tr), used[out.Models[i].Name]
		out.Models[i].Error = tr(out.Models[i].Error)
	}
	// By provider, and in each those that work goes to first.
	slices.SortStableFunc(out.Models, func(a, b modelView) int {
		return cmp.Or(byProvider(a, b), cmp.Compare(boolRank(b.Used), boolRank(a.Used)))
	})
	for i := range out.Agents {
		out.Agents[i].Error = tr(out.Agents[i].Error)
	}
	return out, nil
}

// uses fills in the uses of the projects the caller reaches, all of them or only pid, with the work of pid, and says
// which models some work goes to or is assigned to.
func (s *Service) uses(ctx context.Context, act registry.Actor, pid int64, lang string, out *modelsOut) (map[string]bool, error) {
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
	ws := s.Words(lang)
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
		mws := docs.Words(ws, m, lang)
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
			out.Work = append(out.Work, workView{Kind: "script", Project: p.Name, ProjectTitle: title, Title: manifest.Tr(ws, w.Role),
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
			out.Work = append(out.Work, workView{Kind: "default", Title: manifest.Tr(ws, a.Role), Own: true, Work: b.Grouped(w)})
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
