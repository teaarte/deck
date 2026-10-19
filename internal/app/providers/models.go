package providers

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Units are what a price per million of a model counts: tokens in and out, characters deck sent, or seconds of a
// recording the API reported.
const (
	UnitToken  = "token"
	UnitChar   = "char"
	UnitSecond = "second"
)

type modelView struct {
	Name       string   `json:"name"`
	Connection string   `json:"connection"`
	Model      string   `json:"model"`
	Kind       string   `json:"kind"`
	Agent      string   `json:"agent,omitempty"`
	// Options are the fields deck adds to every call of the model, as JSON.
	Options string `json:"options,omitempty"`
	InputUSD   *float64 `json:"input_usd,omitempty"`
	OutputUSD  *float64 `json:"output_usd,omitempty"`
	CallUSD    *float64 `json:"call_usd,omitempty"`
	Unit       string   `json:"unit"`
	Free       bool     `json:"free"`
	// Subscription is a model paid by the owner's subscription: what a call would cost is an estimate, out of the money spent.
	Subscription bool `json:"subscription"`
	// Builtin is a provider deck was started with; Status, Error, Verdict, Speed and Version are of its last self-check.
	Builtin bool    `json:"builtin"`
	Status  string  `json:"status,omitempty"`
	Error   string  `json:"error,omitempty"`
	Verdict string  `json:"verdict,omitempty"`
	Speed   float64 `json:"speed,omitempty"`
	Version string  `json:"version,omitempty"`
	// Price is how its price reads; Used says some work of a project goes to it now or is assigned to it.
	Price string `json:"price"`
	Used  bool   `json:"used"`
}

// lastCheck fills in the state of a model: of its last self-check in an agent, else of its last call.
func (s *Service) lastCheck(ctx context.Context, workspace int64, v *modelView) {
	if h, err := s.healthOf(ctx, workspace, v.Name); err == nil {
		v.Status, v.Error, v.Verdict, v.Speed, v.Version = h.Status, h.Error, h.Verdict, h.Speed, h.Version
	}
}

// optionsOf are the options of a model as its form shows them: none for an empty object.
func optionsOf(o string) string {
	if strings.TrimSpace(o) == "{}" {
		return ""
	}
	return o
}

// ModelSubject is the health row of a model of a connection, in its workspace.
func ModelSubject(name string) string { return "model:" + name }

type modelsOut struct {
	Agents      []agentView      `json:"agents"`
	Connections []connectionView `json:"connections"`
	Models      []modelView      `json:"models"`
	Assignments []assignmentView `json:"assignments"`
	// Uses are the kinds of work the projects call models for apart from their commands, such as the pictures of
	// their scripts, with the model each goes to.
	Uses []useView `json:"uses"`
	// Work is every command of a project that runs on a model and every kind of work of its scripts, with the model
	// each runs on; given only for one project.
	Work []workView `json:"work"`
}

type useView struct {
	Project string `json:"project"`
	provider.Work
}

// workView is a command or a kind of work of scripts of a project with its model; Own says the project or the
// command has an assignment of its own, else the model is the default.
type workView struct {
	Project string `json:"project"`
	Command string `json:"command,omitempty"`
	Title   string `json:"title"`
	Own     bool   `json:"own"`
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
			v.Key, v.KeySecret = "secret "+c.KeySecret, c.KeySecret
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
	for _, pv := range s.Builtin {
		_, sub := pv.(provider.Subscribed)
		v := modelView{Name: pv.Name(), Kind: RoleText, Agent: pv.Name(), Builtin: true, Subscription: sub}
		s.lastCheck(ctx, act.Workspace, &v)
		out.Models = append(out.Models, v)
	}
	for _, r := range models {
		m := r.Model
		v := modelView{Name: m.Name, Connection: r.Connection.Name, Model: m.Model, Kind: m.Kind, Agent: m.Agent, Options: optionsOf(m.Options),
			InputUSD: nullable(m.InputUsd), OutputUSD: nullable(m.OutputUsd), CallUSD: nullable(m.CallUsd), Unit: m.Unit, Free: m.Free != 0,
			Subscription: m.Subscription != 0}
		s.lastCheck(ctx, act.Workspace, &v)
		out.Models = append(out.Models, v)
	}
	rows, err := s.St.Q.ListAssignments(ctx, act.Workspace)
	if err != nil {
		return out, err
	}
	own := ""
	if p, err := s.St.Q.GetProject(ctx, act.ProjectID); err == nil && act.Confined() {
		own = p.Name
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
			if s.Up(ctx, act.Workspace, name, true) {
				v.Using = name
				break
			}
		}
		out.Assignments = append(out.Assignments, v)
	}
	used, err := s.uses(ctx, act, in, &out)
	if err != nil {
		return out, err
	}
	ws := s.Words(in.Lang)
	b := s.book(ctx, act.Workspace, func(p string) string { return manifest.Tr(ws, p) })
	for i, a := range out.Assignments {
		out.Assignments[i].Choices = b.Of(a.Role)
		if a.Role == RoleAgent {
			out.Assignments[i].Choices = b.Agents()
		}
		if len(a.Models) > 0 {
			out.Assignments[i].Price = b.Price(a.Models[0])
		}
	}
	for i := range out.Models {
		out.Models[i].Price, out.Models[i].Used = Price(out.Models[i], func(p string) string { return manifest.Tr(ws, p) }), used[out.Models[i].Name]
	}
	return out, nil
}

// uses fills in the uses of the projects the caller reaches, and says which models some work goes to or is assigned to.
func (s *Service) uses(ctx context.Context, act registry.Actor, in modelsListIn, out *modelsOut) (map[string]bool, error) {
	used := map[string]bool{}
	for _, a := range out.Assignments {
		for _, name := range a.Models {
			used[name] = true
		}
	}
	pid, err := access.Filter(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	projects, err := s.St.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	ws := s.Words(in.Lang)
	b := s.book(ctx, act.Workspace, func(p string) string { return manifest.Tr(ws, p) })
	for _, p := range projects {
		if pid != 0 && p.ID != pid || act.Confined() && p.ID != act.ProjectID {
			continue
		}
		roles := s.rolesWork(ctx, b, p.ID)
		for _, w := range roles {
			out.Uses = append(out.Uses, useView{Project: p.Name, Work: w})
		}
		if pid == 0 && !act.Confined() {
			continue
		}
		m, err := docs.For(ctx, s.St.Q, registry.Actor{}, p.ID)
		if err != nil {
			continue
		}
		own := func(command, role string) bool {
			return slices.ContainsFunc(out.Assignments, func(a assignmentView) bool {
				return a.Project == p.Name && a.Role == role && (a.Command == command || a.Command == "") && len(a.Models) > 0
			})
		}
		for _, c := range m.Commands {
			if c.Executor.Type == "agent" || c.Executor.Type == "model" {
				w := s.commandWork(ctx, b, p.ID, c.Name)
				out.Work = append(out.Work, workView{Project: p.Name, Command: c.Name, Title: c.Title, Own: own(c.Name, w.Role), Work: w})
			}
		}
		for _, w := range roles {
			out.Work = append(out.Work, workView{Project: p.Name, Title: w.Role, Own: own("", w.Role), Work: w})
		}
	}
	for name := range s.Places(ctx, act.Workspace) {
		used[name] = true
	}
	return used, nil
}

type modelSetIn struct {
	Connection   string  `desc:"Connection the model is reached through"`
	Model        string  `desc:"The model's name in the API, such as google/gemini-3.1-flash-lite-image"`
	Name         string  `opt:"" desc:"How deck names the model in assignments and calls; connection/model when left out"`
	Kind         string  `enum:"text|image|tts|stt|music|video|search" opt:"" desc:"What the model makes, or search for a search service such as SearXNG; a model changed without it keeps its own, a new one takes it from the catalog of the connection, else text"`
	InputUSD     float64 `json:"input_usd" opt:"" desc:"USD per million input tokens, or of the unit; a model changed without a price keeps its own, a new one takes it from the catalog of the connection"`
	OutputUSD    float64 `json:"output_usd" opt:"" desc:"USD per million output tokens; a model priced per character or second has none"`
	Unit         string  `enum:"token|char|second" opt:"" desc:"What the price per million counts: tokens, characters of the text deck sends, or seconds of a recording the API reports; a model changed without it keeps its own, a new one counts tokens"`
	CallUSD      float64 `json:"call_usd" opt:"" desc:"USD per call, for a model priced per picture, clip or request"`
	Subscription *bool   `opt:"" desc:"The owner pays the model by a subscription: deck shows what a call would cost as an estimate and leaves it out of the money spent; a model changed without it keeps its own"`
	Free         *bool   `opt:"" desc:"The model costs nothing, so a trial or a draft calls it without the owner's yes; a model changed without it or a price keeps its own, a new one takes it from the catalog of the connection"`
	Options      string  `type:"json" opt:"" desc:"Fields deck adds to every call of the model of a kind, such as {\"language\": \"ru\"} of speech to text, {\"voice\": \"…\"} of tts, or {\"effort\": \"low\"} of a model of a CLI, the one option it takes; a model changed without them keeps its own, {} clears them"`
	Agent        string  `opt:"" desc:"The agent that runs a text model for agent commands: claude-code or codex, the CLI on the owner's subscription, on the connection of the same name; or an ACP agent inside the sandbox of deck with a shell and files. The name is then the name of a provider. A model changed without it keeps its own"`
	// CheckMinutes give a slow model more time for its self-check.
	CheckMinutes int64 `json:"check_minutes" opt:"" desc:"Minutes the self-check of a model in an agent may take, for a slow one; deck's own ceiling when left out"`
}

func (s *Service) modelSet(ctx context.Context, act registry.Actor, in modelSetIn) (any, error) {
	c, err := s.St.Q.GetConnection(ctx, db.GetConnectionParams{WorkspaceID: act.Workspace, Name: in.Connection})
	if err != nil {
		return nil, registry.NotFound("deck: no connection %q", in.Connection)
	}
	if strings.TrimSpace(in.Model) == "" {
		return nil, registry.Invalid("deck: a model needs its name in the API")
	}
	in.Name = cmp.Or(in.Name, in.Connection+"/"+in.Model)
	if strings.ContainsAny(in.Name, " @") {
		return nil, registry.Invalid("deck: a model name has no spaces or @, got %q", in.Name)
	}
	if c.Api == APICLI {
		in.Agent = cmp.Or(in.Agent, c.Name)
	}
	if in.Agent != "" {
		if err := s.checkAgent(ctx, act.Workspace, c, in); err != nil {
			return nil, err
		}
		in.Kind = RoleText
	}
	m := db.AddModelParams{WorkspaceID: act.Workspace, ConnectionID: c.ID, Name: in.Name, Model: in.Model, Kind: in.Kind, Options: "{}",
		InputUsd: positive(in.InputUSD), OutputUsd: positive(in.OutputUSD), CallUsd: positive(in.CallUSD), Unit: in.Unit,
		Agent: in.Agent, CheckMinutes: in.CheckMinutes}
	unpriced := in.Free == nil && !m.InputUsd.Valid && !m.OutputUsd.Valid && !m.CallUsd.Valid
	if old, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: act.Workspace, Name: in.Name}); err == nil {
		if unpriced {
			m.InputUsd, m.OutputUsd, m.CallUsd, m.Free, unpriced = old.Model.InputUsd, old.Model.OutputUsd, old.Model.CallUsd, old.Model.Free, false
		}
		m.Kind, m.CheckMinutes, m.Unit = cmp.Or(m.Kind, old.Model.Kind), cmp.Or(m.CheckMinutes, old.Model.CheckMinutes), cmp.Or(m.Unit, old.Model.Unit)
		m.Subscription = old.Model.Subscription
	}
	m.Unit = cmp.Or(m.Unit, UnitToken)
	if m.Unit != UnitToken && m.OutputUsd.Valid {
		return nil, registry.Invalid("deck: a model priced per %s has one price, input_usd per million; output_usd is for tokens", m.Unit)
	}
	if c.Api != APICLI && (unpriced || m.Kind == "") {
		if got, ok := s.catalog(ctx, act.Workspace, c, in.Model); ok {
			if unpriced {
				m.InputUsd, m.OutputUsd, m.CallUsd, m.Free = got.InputUsd, got.OutputUsd, got.CallUsd, got.Free
				if cmp.Or(m.Kind, got.Kind) == RoleTTS && in.Unit == "" && m.InputUsd.Valid && !m.OutputUsd.Valid {
					m.Unit = UnitChar
				}
			}
			m.Kind = cmp.Or(m.Kind, got.Kind)
		}
	}
	if in.Free != nil {
		m.Free = store.Bool(*in.Free)
	}
	if in.Subscription != nil {
		m.Subscription = store.Bool(*in.Subscription)
	}
	if c.Api == APICLI {
		m.Subscription = 1
	}
	m.Kind = cmp.Or(m.Kind, RoleText)
	if in.Options != "" {
		var o map[string]any
		if err := json.Unmarshal([]byte(in.Options), &o); err != nil {
			return nil, registry.Invalid("deck: options are a JSON object: %v", err)
		}
		m.Options = in.Options
	}
	var out db.Model
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if old, err := q.GetModel(ctx, db.GetModelParams{WorkspaceID: act.Workspace, Name: in.Name}); err == nil {
			if in.Options == "" {
				m.Options = old.Model.Options
			}
			if in.Agent == "" {
				m.Agent, m.CheckMinutes = old.Model.Agent, old.Model.CheckMinutes
			}
		}
		if in.Agent != "" {
			if err := q.DeleteWorkspaceHealth(ctx, db.DeleteWorkspaceHealthParams{WorkspaceID: store.ID(act.Workspace), Subject: ModelSubject(in.Name)}); err != nil {
				return err
			}
			if err := process.ProviderHealth(ctx, q, in.Name, health.Checking, "the self-check of the model in its agent is about to run"); err != nil {
				return err
			}
		}
		out, err = q.AddModel(ctx, m)
		return err
	})
	if err != nil {
		return nil, err
	}
	if in.Agent != "" {
		s.startCheck(in.Name, true)
	}
	return modelView{Name: out.Name, Connection: c.Name, Model: out.Model, Kind: out.Kind, Agent: out.Agent, Options: optionsOf(out.Options), InputUSD: nullable(out.InputUsd),
		OutputUSD: nullable(out.OutputUsd), CallUSD: nullable(out.CallUsd), Unit: out.Unit, Free: out.Free != 0,
		Subscription: out.Subscription != 0}, nil
}

// checkAgent refuses a model in an agent deck cannot run: an agent it does not know or that is not installed, a
// name that is not one of a provider, an API that is not OpenAI-compatible, a model or effort its CLI does not list.
func (s *Service) checkAgent(ctx context.Context, workspace int64, c db.Connection, in modelSetIn) error {
	cli, _ := s.builtinNamed(in.Agent).(provider.Modeled)
	switch {
	case c.Api == APICLI && in.Agent != c.Name:
		return registry.Invalid("deck: the models of connection %s run in %s, not %s", c.Name, c.Name, in.Agent)
	case cli != nil && c.Api != APICLI:
		return registry.Invalid("deck: a model of %s is on its own connection %s, not %s", in.Agent, in.Agent, c.Name)
	case cli == nil && acp.Agents[in.Agent].Start == nil:
		return registry.Invalid("deck: no agent %q; deck knows %s", in.Agent, strings.Join(s.agentNames(), ", "))
	case !access.ProjectName.MatchString(in.Name) || s.builtinNamed(in.Name) != nil:
		return registry.Invalid("deck: a model in an agent is a provider; name it with lowercase letters, digits and '-', not %q or a built-in provider", in.Name)
	case cli == nil && c.Api != "openai":
		return registry.Invalid("deck: an agent runs a model of an OpenAI-compatible connection, not %s", c.Api)
	case in.CheckMinutes < 0 || in.CheckMinutes > 60:
		return registry.Invalid("deck: check_minutes is 1 to 60, or 0 for deck's own ceiling")
	}
	n, err := s.St.Q.ModelInOtherWorkspace(ctx, db.ModelInOtherWorkspaceParams{Name: in.Name, WorkspaceID: workspace})
	switch {
	case err != nil:
		return err
	case n > 0:
		return registry.Invalid("deck: another workspace has a provider %q; its health and self-check go by name, so pick another", in.Name)
	case cli != nil:
		return s.checkCLI(ctx, workspace, cli, in)
	case !installed(cmp.Or(s.ACPBinaries[in.Agent], in.Agent)):
		return registry.Invalid("deck: %s is not installed on this machine", in.Agent)
	}
	return nil
}

// checkCLI refuses a model its CLI does not list or cannot run deck's turns on, and an effort the model does not take;
// the options of a model of a CLI are its effort alone.
func (s *Service) checkCLI(ctx context.Context, workspace int64, cli provider.Modeled, in modelSetIn) error {
	options := in.Options
	if old, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: in.Name}); err == nil && options == "" {
		options = old.Model.Options
	}
	effort, err := cliEffort(cmp.Or(options, "{}"))
	if err != nil {
		return err
	}
	list, err := cli.Models(ctx)
	if err != nil {
		return registry.Invalid("deck: %v", err)
	}
	var names []string
	for _, m := range list {
		names = append(names, m.Names[0])
		if !slices.Contains(m.Names, in.Model) {
			continue
		}
		switch {
		case m.Refused != "":
			return registry.Invalid("deck: %s: %s", in.Model, m.Refused)
		case effort != "" && len(m.Efforts) == 0:
			return registry.Invalid("deck: %s takes no effort in %s; leave effort out of options", in.Model, in.Agent)
		case effort != "" && !slices.Contains(m.Efforts, effort):
			return registry.Invalid("deck: %s takes effort %s in %s, not %q", in.Model, strings.Join(m.Efforts, ", "), in.Agent, effort)
		}
		return nil
	}
	return registry.Invalid("deck: %s lists no model %q; it lists %s", in.Agent, in.Model, strings.Join(names, ", "))
}

// cliEffort is the effort of the options of a model of a CLI, the one field they take.
func cliEffort(options string) (string, error) {
	var o map[string]any
	if err := json.Unmarshal([]byte(options), &o); err != nil {
		return "", registry.Invalid("deck: options are a JSON object: %v", err)
	}
	effort, ok := o["effort"].(string)
	if len(o) > 1 || len(o) == 1 && !ok {
		return "", registry.Invalid(`deck: the options of a model of a CLI are its effort alone, such as {"effort": "low"}`)
	}
	return effort, nil
}

// agentNames are the agents a model runs in: the built-in CLIs that run other models, then the ACP agents.
func (s *Service) agentNames() []string {
	var out []string
	for _, pv := range s.Builtin {
		if _, ok := pv.(provider.Modeled); ok {
			out = append(out, pv.Name())
		}
	}
	return append(out, acp.Names()...)
}

func positive(f float64) sql.NullFloat64 { return sql.NullFloat64{Float64: f, Valid: f > 0} }

// catalog reads the price and kind of a model from OpenRouter's list of its connection; a zero is free only for a model
// of text or one named free, since a song or a picture may cost what no field of the catalog says.
func (s *Service) catalog(ctx context.Context, workspace int64, c db.Connection, model string) (db.AddModelParams, bool) {
	var out db.AddModelParams
	key := ""
	if c.KeySecret != "" {
		key, _ = s.Secret(workspace, c.KeySecret)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := gateway.SendModel(ctx, gateway.ModelTarget{URL: c.Url, API: c.Api, Key: strings.TrimSpace(key)}, gateway.ModelCall{Method: http.MethodGet, Path: "models"})
	if err != nil {
		return out, false
	}
	defer resp.Body.Close()
	var list struct {
		Data []struct {
			ID           string         `json:"id"`
			Pricing      map[string]any `json:"pricing"`
			Architecture struct {
				Output []string `json:"output_modalities"`
			} `json:"architecture"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&list) != nil {
		return out, false
	}
	perMillion := func(v string) sql.NullFloat64 {
		f, err := strconv.ParseFloat(v, 64)
		return sql.NullFloat64{Float64: f * 1e6, Valid: err == nil && f > 0}
	}
	for _, d := range list.Data {
		if d.ID != model {
			continue
		}
		price := func(k string) string { v, _ := d.Pricing[k].(string); return v }
		out.InputUsd, out.OutputUsd = perMillion(price("prompt")), perMillion(price("completion"))
		if f, err := strconv.ParseFloat(price("request"), 64); err == nil && f > 0 {
			out.CallUsd = sql.NullFloat64{Float64: f, Valid: true}
		}
		free := strings.HasSuffix(d.ID, ":free") || len(d.Pricing) > 0 && !slices.ContainsFunc(d.Architecture.Output, func(m string) bool { return m != "text" })
		for k := range d.Pricing {
			if f, err := strconv.ParseFloat(price(k), 64); err == nil && f != 0 {
				free = false
			}
		}
		out.Free = store.Bool(free)
		switch {
		case slices.Contains(d.Architecture.Output, "video"):
			out.Kind = RoleVideo
		case slices.Contains(d.Architecture.Output, "image"):
			out.Kind = RoleImage
		case slices.Contains(d.Architecture.Output, "audio"):
			out.Kind = RoleTTS
		case len(d.Architecture.Output) > 0:
			out.Kind = RoleText
		}
		return out, true
	}
	return out, false
}

type modelRemoveIn struct {
	Name string `desc:"Model name"`
}

func (s *Service) modelRemove(ctx context.Context, act registry.Actor, in modelRemoveIn) (any, error) {
	var n int64
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		active, err := q.CountActiveOnProvider(ctx, db.CountActiveOnProviderParams{Provider: in.Name, WorkspaceID: act.Workspace})
		if err != nil {
			return err
		}
		if active > 0 {
			return registry.Invalid("deck: %d processes still run on %s; stop them or let them finish first", active, in.Name)
		}
		if n, err = q.DeleteModel(ctx, db.DeleteModelParams{WorkspaceID: act.Workspace, Name: in.Name}); err != nil || n == 0 {
			return err
		}
		return q.DeleteModelHealth(ctx, db.DeleteModelHealthParams{WorkspaceID: store.ID(act.Workspace), Name: in.Name})
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("deck: no model %q", in.Name)
	}
	return map[string]any{"removed": in.Name}, nil
}
