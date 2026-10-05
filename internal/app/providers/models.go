package providers

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"deck2/internal/app/access"
	"deck2/internal/gateway"
	"deck2/internal/process"
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
	Key   string `json:"key"`
	Local bool   `json:"local"`
	// DailyUSD is the most its models may cost in a day; none without a limit.
	DailyUSD *float64 `json:"daily_usd,omitempty"`
	Models   int64    `json:"models"`
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
	Free       bool     `json:"free"`
	Status     string   `json:"status,omitempty"`
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

func nullable(f sql.NullFloat64) *float64 {
	if !f.Valid {
		return nil
	}
	return &f.Float64
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
		v := connectionView{Name: c.Name, API: c.Api, URL: c.Url, Key: "none", Local: c.Local != 0, DailyUSD: nullable(c.DailyUsd)}
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
	for _, r := range models {
		m := r.Model
		v := modelView{Name: m.Name, Connection: r.Connection.Name, Model: m.Model, Kind: m.Kind, Agent: m.Agent,
			InputUSD: nullable(m.InputUsd), OutputUSD: nullable(m.OutputUsd), CallUSD: nullable(m.CallUsd), Free: m.Free != 0}
		if h, err := s.St.Q.GetHealth(ctx, process.HealthSubject(m.Name)); err == nil {
			v.Status = h.Status
		}
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
	for _, name := range in.Models {
		if in.Role == RoleAgent {
			if _, err := s.Provider(ctx, act.Workspace, name); err != nil {
				return nil, err
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

// connectionFor is the connection of an address and key in the workspace, added under a name of its own when
// there is none yet.
func connectionFor(ctx context.Context, q *db.Queries, workspace int64, rawURL, keySecret string, local bool) (db.Connection, error) {
	c, err := q.FindConnection(ctx, db.FindConnectionParams{WorkspaceID: workspace, Url: rawURL, KeySecret: keySecret})
	if err == nil {
		if local && c.Local == 0 {
			c.Local = 1
			return q.AddConnection(ctx, db.AddConnectionParams{WorkspaceID: workspace, Name: c.Name, Api: c.Api, Url: c.Url, KeySecret: c.KeySecret,
				Local: 1, DailyUsd: c.DailyUsd})
		}
		return c, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}
	base := connectionName(rawURL, keySecret)
	name := base
	for n := 2; ; n++ {
		if _, err := q.GetConnection(ctx, db.GetConnectionParams{WorkspaceID: workspace, Name: name}); errors.Is(err, sql.ErrNoRows) {
			break
		}
		name = fmt.Sprintf("%s-%d", base, n)
	}
	return q.AddConnection(ctx, db.AddConnectionParams{WorkspaceID: workspace, Name: name, Api: "openai", Url: rawURL, KeySecret: keySecret, Local: b2i(local)})
}

// connectionName is a name for a new connection: its key's secret, else local for this machine and the network
// of the owner, else the name of the host's site.
func connectionName(rawURL, keySecret string) string {
	if name := strings.ToLower(strings.NewReplacer("_", "-", ".", "-").Replace(keySecret)); access.ProjectName.MatchString(name) {
		return name
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "connection"
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil || host == "localhost" || !strings.Contains(host, ".") {
		return "local"
	}
	labels := strings.Split(host, ".")
	name := strings.ToLower(labels[max(0, len(labels)-2)])
	if !access.ProjectName.MatchString(name) {
		return "connection"
	}
	return name
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

type connectionSetIn struct {
	Name      string  `desc:"Connection name: lowercase letters, digits and '-'"`
	API       string  `json:"api" enum:"openai|google|anthropic|raw" desc:"What the API speaks: openai for OpenAI-compatible ones (OpenRouter, Ollama, Groq, NVIDIA), google and anthropic for their own, raw for a service deck passes the key to as a bearer token"`
	URL       string  `desc:"Base URL of the API, such as https://openrouter.ai/api/v1"`
	KeySecret string  `json:"key_secret" opt:"" desc:"Name of the secret with the API key; a connection changed without it keeps its own"`
	Key       string  `type:"secret" opt:"" desc:"The API key; deck keeps it in the secret key_secret names, or one named after the connection"`
	Local     bool    `opt:"" desc:"The models run on the owner's own hardware"`
	DailyUSD  float64 `json:"daily_usd" opt:"" desc:"The most its models may cost in a day in USD; past it a call waits for the owner; 0 for no limit"`
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
	case in.DailyUSD < 0:
		return nil, registry.Invalid("deck: daily_usd is a sum in USD, or 0 for no limit")
	}
	if in.Key != "" {
		in.KeySecret = cmp.Or(in.KeySecret, in.Name)
		if err := s.WriteSecret(act.Workspace, in.KeySecret, strings.TrimSpace(in.Key)); err != nil {
			return nil, err
		}
	}
	limit := sql.NullFloat64{Float64: in.DailyUSD, Valid: in.DailyUSD > 0}
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if old, err := q.GetConnection(ctx, db.GetConnectionParams{WorkspaceID: act.Workspace, Name: in.Name}); err == nil && in.KeySecret == "" {
			in.KeySecret = old.KeySecret
		}
		_, err := q.AddConnection(ctx, db.AddConnectionParams{WorkspaceID: act.Workspace, Name: in.Name, Api: in.API, Url: in.URL,
			KeySecret: in.KeySecret, Local: b2i(in.Local), DailyUsd: limit})
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
	Kind       string  `enum:"text|image|tts|stt" opt:"" desc:"What the model makes; from the catalog of the connection, else text"`
	InputUSD   float64 `json:"input_usd" opt:"" desc:"USD per million input tokens; from the catalog of the connection when left out"`
	OutputUSD  float64 `json:"output_usd" opt:"" desc:"USD per million output tokens"`
	CallUSD    float64 `json:"call_usd" opt:"" desc:"USD per call, for a model priced per picture, clip or request"`
	Free       bool    `opt:"" desc:"The model costs nothing"`
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
	m := db.AddModelParams{WorkspaceID: act.Workspace, ConnectionID: c.ID, Name: in.Name, Model: in.Model, Kind: in.Kind, Options: "{}",
		InputUsd: positive(in.InputUSD), OutputUsd: positive(in.OutputUSD), CallUsd: positive(in.CallUSD), Free: b2i(in.Free)}
	if !in.Free && !m.InputUsd.Valid && !m.OutputUsd.Valid && !m.CallUsd.Valid || m.Kind == "" {
		if got, ok := s.catalog(ctx, act.Workspace, c, in.Model); ok {
			if !in.Free && !m.InputUsd.Valid && !m.OutputUsd.Valid && !m.CallUsd.Valid {
				m.InputUsd, m.OutputUsd, m.CallUsd, m.Free = got.InputUsd, got.OutputUsd, got.CallUsd, got.Free
			}
			m.Kind = cmp.Or(m.Kind, got.Kind)
		}
	}
	m.Kind = cmp.Or(m.Kind, RoleText)
	var out db.Model
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if old, err := q.GetModel(ctx, db.GetModelParams{WorkspaceID: act.Workspace, Name: in.Name}); err == nil {
			m.Agent, m.Options, m.CheckMinutes = old.Model.Agent, old.Model.Options, old.Model.CheckMinutes
		}
		out, err = q.AddModel(ctx, m)
		return err
	})
	if err != nil {
		return nil, err
	}
	return modelView{Name: out.Name, Connection: c.Name, Model: out.Model, Kind: out.Kind, Agent: out.Agent, InputUSD: nullable(out.InputUsd),
		OutputUSD: nullable(out.OutputUsd), CallUSD: nullable(out.CallUsd), Free: out.Free != 0}, nil
}

func positive(f float64) sql.NullFloat64 { return sql.NullFloat64{Float64: f, Valid: f > 0} }

// catalog reads the price and kind of a model from the list of models of its connection, as OpenRouter gives them:
// USD per token as text, and the output modalities.
func (s *Service) catalog(ctx context.Context, workspace int64, c db.Connection, model string) (db.AddModelParams, bool) {
	var out db.AddModelParams
	key := ""
	if c.KeySecret != "" {
		key, _ = s.Secret(workspace, c.KeySecret)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := gateway.SendModel(ctx, gateway.ModelTarget{URL: c.Url, API: c.Api, Key: strings.TrimSpace(key)}, http.MethodGet, "models", "", nil, nil)
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
				Image      string `json:"image"`
			} `json:"pricing"`
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
		out.InputUsd, out.OutputUsd = perMillion(d.Pricing.Prompt), perMillion(d.Pricing.Completion)
		if f, err := strconv.ParseFloat(d.Pricing.Request, 64); err == nil && f > 0 {
			out.CallUsd = sql.NullFloat64{Float64: f, Valid: true}
		}
		out.Free = b2i(d.Pricing.Prompt == "0" && d.Pricing.Completion == "0" && !out.CallUsd.Valid)
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
		n, err = q.DeleteModel(ctx, db.DeleteModelParams{WorkspaceID: act.Workspace, Name: in.Name})
		return err
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("deck: no model %q", in.Name)
	}
	return map[string]any{"removed": in.Name}, nil
}

type projectModelLimitIn struct {
	Project  string  `type:"project" desc:"Project name"`
	DailyUSD float64 `json:"daily_usd" desc:"The most the models its commands call may cost in a day in USD; 0 for no limit of its own"`
}

func (s *Service) projectModelLimit(ctx context.Context, act registry.Actor, in projectModelLimitIn) (any, error) {
	p, err := s.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: in.Project})
	if err != nil {
		return nil, registry.NotFound("deck: no project %q", in.Project)
	}
	if in.DailyUSD < 0 {
		return nil, registry.Invalid("deck: daily_usd is a sum in USD, or 0 for no limit")
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.SetProjectModelLimit(ctx, db.SetProjectModelLimitParams{DailyUsd: positive(in.DailyUSD), ID: p.ID})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": in.Project, "daily_usd": in.DailyUSD}, nil
}
