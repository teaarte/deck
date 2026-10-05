package providers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"

	"deck2/internal/app/access"
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
	// Open lets modules call its models without asking while the day stays within the limit.
	Open   bool  `json:"open"`
	Models int64 `json:"models"`
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
	out := modelsOut{Connections: []connectionView{}, Models: []modelView{}, Assignments: []assignmentView{}}
	conns, err := s.St.Q.ListConnections(ctx, act.Workspace)
	if err != nil {
		return out, err
	}
	models, err := s.St.Q.ListModels(ctx, act.Workspace)
	if err != nil {
		return out, err
	}
	for _, c := range conns {
		v := connectionView{Name: c.Name, API: c.Api, URL: c.Url, Key: "none", Local: c.Local != 0, DailyUSD: nullable(c.DailyUsd), Open: c.Open != 0}
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
				Local: 1, DailyUsd: c.DailyUsd, Open: c.Open})
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
	if keySecret != "" && access.ProjectName.MatchString(keySecret) {
		return keySecret
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
