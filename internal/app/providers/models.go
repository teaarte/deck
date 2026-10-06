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
)

var roles = []string{RoleAgent, RoleText, RoleImage, RoleTTS, RoleSTT}

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
	Name       string `json:"name"`
	Connection string `json:"connection"`
	Model      string `json:"model"`
	Kind       string `json:"kind"`
	Agent      string `json:"agent,omitempty"`
	Free       bool   `json:"free"`
	// Builtin is a provider deck was started with; Status, Error, Verdict, Speed and Version are of its last self-check.
	Builtin bool    `json:"builtin"`
	Status  string  `json:"status,omitempty"`
	Error   string  `json:"error,omitempty"`
	Verdict string  `json:"verdict,omitempty"`
	Speed   float64 `json:"speed,omitempty"`
	Version string  `json:"version,omitempty"`
}

// lastCheck fills in the state of a model's last self-check.
func (s *Service) lastCheck(ctx context.Context, v *modelView) {
	if h, err := s.St.Q.GetHealth(ctx, process.HealthSubject(v.Name)); err == nil {
		v.Status, v.Error, v.Verdict, v.Speed, v.Version = h.Status, h.Error, h.Verdict, h.Speed, h.Version
	}
}

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

func (s *Service) modelsList(ctx context.Context, act registry.Actor, _ struct{}) (modelsOut, error) {
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
		s.lastCheck(ctx, &v)
		out.Models = append(out.Models, v)
	}
	for _, r := range models {
		m := r.Model
		v := modelView{Name: m.Name, Connection: r.Connection.Name, Model: m.Model, Kind: m.Kind, Agent: m.Agent, Free: m.Free != 0}
		s.lastCheck(ctx, &v)
		out.Models = append(out.Models, v)
	}
	rows, err := s.St.Q.ListAssignments(ctx, act.Workspace)
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		v := assignmentView{Project: r.Project, Command: r.Command, Role: r.Role}
		_ = json.Unmarshal([]byte(r.Models), &v.Models)
		for _, name := range v.Models {
			if s.up(ctx, name) {
				v.Using = name
				break
			}
		}
		out.Assignments = append(out.Assignments, v)
	}
	return out, nil
}

type assignmentSetIn struct {
	Role    string   `enum:"agent|text|image|tts|stt" desc:"The work: agent turns, or direct calls of text, image, tts (speech) or stt (speech to text)"`
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

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
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
			KeySecret: in.KeySecret, Local: b2i(in.Local)})
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
	Connection string `desc:"Connection the model is reached through"`
	Model      string `desc:"The model's name in the API, such as google/gemini-3.1-flash-lite-image"`
	Name       string `opt:"" desc:"How deck names the model in assignments and calls; connection/model when left out"`
	Kind       string `enum:"text|image|tts|stt" opt:"" desc:"What the model makes; a model changed without it keeps its own, a new one takes it from the catalog of the connection, else text"`
	Free       bool   `opt:"" desc:"The model costs nothing, so a trial or a draft calls it without the owner's yes; a new model added without a kind takes it from the catalog of the connection"`
	Options    string `type:"json" opt:"" desc:"Fields deck adds to every call of the model of a kind, such as {\"language\": \"ru\"} of speech to text; a model changed without them keeps its own"`
	Agent      string `opt:"" desc:"The ACP agent that runs a text model inside the sandbox of deck with a shell and files, for agent commands; the name is then the name of a provider. A model changed without it keeps its own"`
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
		Free: b2i(in.Free), Agent: in.Agent, CheckMinutes: in.CheckMinutes}
	if old, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: act.Workspace, Name: in.Name}); err == nil {
		m.Kind, m.CheckMinutes, m.Free = cmp.Or(m.Kind, old.Model.Kind), cmp.Or(m.CheckMinutes, old.Model.CheckMinutes), max(m.Free, old.Model.Free)
	}
	if m.Kind == "" {
		if got, ok := s.catalog(ctx, act.Workspace, c, in.Model); ok {
			m.Kind, m.Free = got.Kind, max(m.Free, got.Free)
		}
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
			if err := health.Set(ctx, q, health.Of{}, process.HealthSubject(in.Name), "checking", "the self-check of the model in its agent is about to run"); err != nil {
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
	return modelView{Name: out.Name, Connection: c.Name, Model: out.Model, Kind: out.Kind, Agent: out.Agent, Free: out.Free != 0}, nil
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

// catalog reads the kind of a model and whether it is free from the list of models of its connection, as OpenRouter
// gives them: the output modalities, and a price of zero.
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
			ID      string `json:"id"`
			Pricing struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
				Request    string `json:"request"`
			} `json:"pricing"`
			Architecture struct {
				Output []string `json:"output_modalities"`
			} `json:"architecture"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&list) != nil {
		return out, false
	}
	for _, d := range list.Data {
		if d.ID != model {
			continue
		}
		out.Free = b2i(d.Pricing.Prompt == "0" && d.Pricing.Completion == "0" && cmp.Or(d.Pricing.Request, "0") == "0")
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
		return q.DeleteModelHealth(ctx, in.Name)
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("deck: no model %q", in.Name)
	}
	return map[string]any{"removed": in.Name}, nil
}
