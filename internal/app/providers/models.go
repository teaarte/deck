package providers

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"deck2/internal/app/access"
	"deck2/internal/gateway"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/provider/acp"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Roles are the kinds of work an assignment names models for: agent turns, and direct calls by kind.
const (
	RoleAgent = "agent"
	RoleText  = "text"
	RoleImage = "image"
	RoleTTS   = "tts"
	RoleSTT   = "stt"
	RoleMusic = "music"
)

var roles = []string{RoleAgent, RoleText, RoleImage, RoleTTS, RoleSTT, RoleMusic}

// Units are what a price per million of a model counts: tokens in and out, characters deck sent, or seconds of a
// recording the API reported.
const (
	UnitToken  = "token"
	UnitChar   = "char"
	UnitSecond = "second"
)

// Assigned are the models of a role for a command, in order: its own assignment, else its project's, else the
// workspace's; none when nothing is assigned.
func Assigned(ctx context.Context, q *db.Queries, workspace, project int64, command, role string) ([]string, error) {
	type place struct {
		project int64
		command string
	}
	places := []place{{0, ""}}
	if project != 0 {
		places = append([]place{{project, ""}}, places...)
		if command != "" {
			places = append([]place{{project, command}}, places...)
		}
	}
	for _, at := range places {
		raw, err := q.GetAssignment(ctx, db.GetAssignmentParams{WorkspaceID: workspace, ProjectID: at.project, Command: at.command, Role: role})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var names []string
		if json.Unmarshal([]byte(raw), &names) == nil && len(names) > 0 {
			return names, nil
		}
	}
	return nil, nil
}

type connectionView struct {
	Name string `json:"name"`
	API  string `json:"api"`
	URL  string `json:"url"`
	// Key is the secret the API key is kept in, or none.
	Key    string `json:"key"`
	Local  bool   `json:"local"`
	Models int64  `json:"models"`
}

type modelView struct {
	Name       string   `json:"name"`
	Connection string   `json:"connection"`
	Model      string   `json:"model"`
	Kind       string   `json:"kind"`
	Agent      string   `json:"agent,omitempty"`
	InputUSD   *float64 `json:"input_usd,omitempty"`
	OutputUSD  *float64 `json:"output_usd,omitempty"`
	CallUSD    *float64 `json:"call_usd,omitempty"`
	Unit       string   `json:"unit"`
	Free       bool     `json:"free"`
	// Builtin is a provider deck was started with; Status, Error, Verdict, Speed and Version are of its last self-check.
	Builtin bool    `json:"builtin"`
	Status  string  `json:"status,omitempty"`
	Error   string  `json:"error,omitempty"`
	Verdict string  `json:"verdict,omitempty"`
	Speed   float64 `json:"speed,omitempty"`
	Version string  `json:"version,omitempty"`
}

// lastCheck fills in the state of a model: of its last self-check in an agent, else of its last call.
func (s *Service) lastCheck(ctx context.Context, workspace int64, v *modelView) {
	h, err := s.St.Q.GetHealth(ctx, process.HealthSubject(v.Name))
	if v.Agent == "" {
		h, err = s.St.Q.GetWorkspaceHealth(ctx, db.GetWorkspaceHealthParams{WorkspaceID: store.ID(workspace), Subject: ModelSubject(v.Name)})
	}
	if err == nil {
		v.Status, v.Error, v.Verdict, v.Speed, v.Version = h.Status, h.Error, h.Verdict, h.Speed, h.Version
	}
}

// ModelSubject is the health row of a model of a connection, in its workspace.
func ModelSubject(name string) string { return "model:" + name }

type assignmentView struct {
	Project string   `json:"project,omitempty"`
	Command string   `json:"command,omitempty"`
	Role    string   `json:"role"`
	Models  []string `json:"models"`
	// Using is the model that takes the work now: the first that is up, a fallback when the first is down.
	Using string `json:"using,omitempty"`
}

type modelsOut struct {
	Agents      []agentView      `json:"agents"`
	Connections []connectionView `json:"connections"`
	Models      []modelView      `json:"models"`
	Assignments []assignmentView `json:"assignments"`
}

func nullable(f sql.NullFloat64) *float64 {
	if !f.Valid {
		return nil
	}
	return &f.Float64
}

type modelsListIn struct {
	Project string `type:"project" opt:"" desc:"Only the assignments of this project and its commands"`
}

func (s *Service) modelsList(ctx context.Context, act registry.Actor, in modelsListIn) (modelsOut, error) {
	out := modelsOut{Agents: s.agents(ctx), Connections: []connectionView{}, Models: []modelView{}, Assignments: []assignmentView{}}
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
		if c.KeySecret != "" {
			v.Key = "secret " + c.KeySecret
		}
		for _, m := range models {
			if m.Model.ConnectionID == c.ID {
				v.Models++
			}
		}
		out.Connections = append(out.Connections, v)
	}
	for _, pv := range s.Builtin {
		v := modelView{Name: pv.Name(), Kind: RoleText, Agent: pv.Name(), Builtin: true}
		s.lastCheck(ctx, act.Workspace, &v)
		out.Models = append(out.Models, v)
	}
	for _, r := range models {
		m := r.Model
		v := modelView{Name: m.Name, Connection: r.Connection.Name, Model: m.Model, Kind: m.Kind, Agent: m.Agent,
			InputUSD: nullable(m.InputUsd), OutputUSD: nullable(m.OutputUsd), CallUSD: nullable(m.CallUsd), Unit: m.Unit, Free: m.Free != 0}
		s.lastCheck(ctx, act.Workspace, &v)
		out.Models = append(out.Models, v)
	}
	rows, err := s.St.Q.ListAssignments(ctx, act.Workspace)
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		if in.Project != "" && r.Project != in.Project {
			continue
		}
		v := assignmentView{Project: r.Project, Command: r.Command, Role: r.Role}
		_ = json.Unmarshal([]byte(r.Models), &v.Models)
		for _, name := range v.Models {
			if s.up(ctx, act.Workspace, name) {
				v.Using = name
				break
			}
		}
		out.Assignments = append(out.Assignments, v)
	}
	return out, nil
}

type assignmentSetIn struct {
	Role    string   `enum:"agent|text|image|tts|stt|music" desc:"The work: agent turns, or direct calls of text, image, tts (speech), stt (speech to text) or music"`
	Project string   `type:"project" opt:"" desc:"Project the assignment is for; the whole workspace without it"`
	Command string   `opt:"" desc:"Command of the project the assignment is for; the whole project without it"`
	Models  []string `opt:"" desc:"Names of models or built-in providers in order: the first, then the fallbacks; empty removes the assignment"`
}

func (s *Service) assignmentSet(ctx context.Context, act registry.Actor, in assignmentSetIn) (any, error) {
	if in.Command != "" && in.Project == "" {
		return nil, registry.Invalid("deck: an assignment of a command names its project too")
	}
	var project int64
	if in.Project != "" {
		p, err := s.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: in.Project})
		if err != nil {
			return nil, registry.NotFound("deck: no project %q", in.Project)
		}
		project = p.ID
	}
	var cmd *manifest.Command
	if in.Role == RoleAgent && in.Command != "" {
		var err error
		if _, cmd, err = s.agentCommand(ctx, act.Workspace, in.Project+"."+in.Command); err != nil {
			return nil, err
		}
	}
	for _, name := range in.Models {
		if in.Role == RoleAgent {
			pv, err := s.Provider(ctx, act.Workspace, name)
			if err != nil {
				return nil, err
			}
			if cmd == nil {
				continue
			}
			if missing := pv.Caps().Missing(cmd.Requires); len(missing) > 0 {
				return nil, registry.Invalid("deck: provider %s does not fit %s.%s: it lacks %s", name, in.Project, in.Command, strings.Join(missing, ", "))
			}
			continue
		}
		row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: act.Workspace, Name: name})
		if err != nil {
			return nil, registry.NotFound("deck: no model %q", name)
		}
		if row.Model.Kind != in.Role {
			return nil, registry.Invalid("deck: model %s is of kind %s, not %s", name, row.Model.Kind, in.Role)
		}
	}
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if len(in.Models) == 0 {
			return q.DeleteAssignment(ctx, db.DeleteAssignmentParams{WorkspaceID: act.Workspace, ProjectID: project, Command: in.Command, Role: in.Role})
		}
		models, _ := json.Marshal(slices.Compact(in.Models))
		return q.SetAssignment(ctx, db.SetAssignmentParams{WorkspaceID: act.Workspace, ProjectID: store.ID(project), Command: in.Command, Role: in.Role, Models: string(models)})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"role": in.Role, "project": in.Project, "command": in.Command, "models": in.Models}, nil
}

type connectionSetIn struct {
	Name      string `desc:"Connection name: lowercase letters, digits and '-'"`
	API       string `json:"api" enum:"openai|google|anthropic|raw" desc:"What the API speaks: openai for OpenAI-compatible ones (OpenRouter, Ollama, Groq, NVIDIA), google and anthropic for their own, raw for a service deck passes the key to as a bearer token"`
	URL       string `desc:"Base URL of the API, such as https://openrouter.ai/api/v1"`
	KeySecret string `json:"key_secret" opt:"" desc:"Name of the secret with the API key; a connection changed without it keeps its own"`
	Key       string `type:"secret" opt:"" desc:"The API key; deck keeps it in the secret key_secret names, or one named after the connection"`
	Local     bool   `opt:"" desc:"The models run on the owner's own hardware"`
}

func (s *Service) connectionSet(ctx context.Context, act registry.Actor, in connectionSetIn) (any, error) {
	switch {
	case !access.ProjectName.MatchString(in.Name):
		return nil, registry.Invalid("deck: connection name %q: lowercase letters, digits and '-'", in.Name)
	case !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://"):
		return nil, registry.Invalid("deck: the url of a connection is http or https, got %q", in.URL)
	case strings.ContainsAny(in.URL, "@?#"):
		return nil, registry.Invalid("deck: the url takes no user, query or key; give the key in key, deck keeps it in a secret")
	case in.KeySecret != "" && !access.ValidSecret(in.KeySecret):
		return nil, registry.Invalid("deck: bad secret name %q", in.KeySecret)
	}
	if in.Key != "" {
		in.KeySecret = cmp.Or(in.KeySecret, in.Name)
		if err := s.WriteSecret(act.Workspace, in.KeySecret, strings.TrimSpace(in.Key)); err != nil {
			return nil, err
		}
	}
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if old, err := q.GetConnection(ctx, db.GetConnectionParams{WorkspaceID: act.Workspace, Name: in.Name}); err == nil && in.KeySecret == "" {
			in.KeySecret = old.KeySecret
		}
		_, err := q.AddConnection(ctx, db.AddConnectionParams{WorkspaceID: act.Workspace, Name: in.Name, Api: in.API, Url: in.URL,
			KeySecret: in.KeySecret, Local: store.Bool(in.Local)})
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"connection": in.Name, "key_secret": in.KeySecret}, nil
}

type connectionRemoveIn struct {
	Name string `desc:"Connection name"`
}

func (s *Service) connectionRemove(ctx context.Context, act registry.Actor, in connectionRemoveIn) (any, error) {
	var n int64
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		models, err := q.ListModels(ctx, act.Workspace)
		if err != nil {
			return err
		}
		for _, m := range models {
			if m.Connection.Name == in.Name {
				return registry.Invalid("deck: model %s is on connection %s; remove the models first", m.Model.Name, in.Name)
			}
		}
		n, err = q.DeleteConnection(ctx, db.DeleteConnectionParams{WorkspaceID: act.Workspace, Name: in.Name})
		return err
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("deck: no connection %q", in.Name)
	}
	return map[string]any{"removed": in.Name}, nil
}

type modelSetIn struct {
	Connection string  `desc:"Connection the model is reached through"`
	Model      string  `desc:"The model's name in the API, such as google/gemini-3.1-flash-lite-image"`
	Name       string  `opt:"" desc:"How deck names the model in assignments and calls; connection/model when left out"`
	Kind       string  `enum:"text|image|tts|stt|music" opt:"" desc:"What the model makes; a model changed without it keeps its own, a new one takes it from the catalog of the connection, else text"`
	InputUSD   float64 `json:"input_usd" opt:"" desc:"USD per million input tokens, or of the unit; a model changed without a price keeps its own, a new one takes it from the catalog of the connection"`
	OutputUSD  float64 `json:"output_usd" opt:"" desc:"USD per million output tokens; a model priced per character or second has none"`
	Unit       string  `enum:"token|char|second" opt:"" desc:"What the price per million counts: tokens, characters of the text deck sends, or seconds of a recording the API reports; a model changed without it keeps its own, a new one counts tokens"`
	CallUSD    float64 `json:"call_usd" opt:"" desc:"USD per call, for a model priced per picture, clip or request"`
	Free       *bool   `opt:"" desc:"The model costs nothing, so a trial or a draft calls it without the owner's yes; a model changed without it or a price keeps its own, a new one takes it from the catalog of the connection"`
	Options    string  `type:"json" opt:"" desc:"Fields deck adds to every call of the model of a kind, such as {\"language\": \"ru\"} of speech to text; a model changed without them keeps its own"`
	Agent      string  `opt:"" desc:"The ACP agent that runs a text model inside the sandbox of deck with a shell and files, for agent commands; the name is then the name of a provider. A model changed without it keeps its own"`
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
	}
	m.Unit = cmp.Or(m.Unit, UnitToken)
	if m.Unit != UnitToken && m.OutputUsd.Valid {
		return nil, registry.Invalid("deck: a model priced per %s has one price, input_usd per million; output_usd is for tokens", m.Unit)
	}
	if unpriced || m.Kind == "" {
		if got, ok := s.catalog(ctx, act.Workspace, c, in.Model); ok {
			if unpriced {
				m.InputUsd, m.OutputUsd, m.CallUsd, m.Free = got.InputUsd, got.OutputUsd, got.CallUsd, got.Free
			}
			m.Kind = cmp.Or(m.Kind, got.Kind)
		}
	}
	if in.Free != nil {
		m.Free = store.Bool(*in.Free)
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
	return modelView{Name: out.Name, Connection: c.Name, Model: out.Model, Kind: out.Kind, Agent: out.Agent, InputUSD: nullable(out.InputUsd),
		OutputUSD: nullable(out.OutputUsd), CallUSD: nullable(out.CallUsd), Unit: out.Unit, Free: out.Free != 0}, nil
}

// checkAgent refuses a model in an agent deck cannot run: an agent it does not know or that is not installed, a
// name that is not one of a provider, an API that is not OpenAI-compatible.
func (s *Service) checkAgent(ctx context.Context, workspace int64, c db.Connection, in modelSetIn) error {
	switch {
	case acp.Agents[in.Agent].Start == nil:
		return registry.Invalid("deck: no ACP agent %q; deck knows %s", in.Agent, strings.Join(acp.Names(), ", "))
	case !access.ProjectName.MatchString(in.Name) || s.builtinNamed(in.Name) != nil:
		return registry.Invalid("deck: a model in an agent is a provider; name it with lowercase letters, digits and '-', not %q or a built-in provider", in.Name)
	case c.Api != "openai":
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
	case !installed(cmp.Or(s.ACPBinaries[in.Agent], in.Agent)):
		return registry.Invalid("deck: %s is not installed on this machine", in.Agent)
	}
	return nil
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
		free := len(d.Pricing) > 0 && (strings.HasSuffix(d.ID, ":free") || !slices.ContainsFunc(d.Architecture.Output, func(m string) bool { return m != "text" }))
		for k := range d.Pricing {
			if f, err := strconv.ParseFloat(price(k), 64); err == nil && f != 0 {
				free = false
			}
		}
		out.Free = store.Bool(free)
		switch {
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
