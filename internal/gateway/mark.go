package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
	"deck2/master"
)

// turn is the process of an agent's turn or the master's a call is made for, now or later as a button of its note, which
// deck marks by what it read; false for a program, a trigger, a token, the owner and the turn's own models.
func (g *Gateway) turn(ctx context.Context, act registry.Actor) (db.Process, bool) {
	if ctx.Value(modelsKey{}) != nil || act.ProcessID == 0 || act.Run != "" || act.Kind != registry.Agent && act.Kind != registry.Master {
		return db.Process{}, false
	}
	p, err := g.st.Q.GetProcess(ctx, act.ProcessID)
	return p, err == nil
}

// modelsKey marks a call of a model through deck's proxy, which is no way out of the turn that thinks with it.
type modelsKey struct{}

// conduit is a way out the module of c holds always, which a call of c hands its input to: any of a script or a tool
// of the module, the network of its agents' shells of an agent, whose other ways out deck marks by that input.
func (g *Gateway) conduit(ctx context.Context, c *registry.Command) (registry.Need, bool, error) {
	if c.ProjectID == 0 {
		return registry.Need{}, false, nil
	}
	p, err := g.st.Q.GetProject(ctx, c.ProjectID)
	if err != nil {
		return registry.Need{}, false, err
	}
	m, granted, err := docs.Rights(ctx, g.st.Q, c.ProjectID)
	if err != nil {
		return registry.Need{}, false, err
	}
	cmd := m.Command(strings.TrimPrefix(c.Name, p.Name+"."))
	if cmd == nil || !slices.Contains([]string{"script", "agent", "tool"}, cmd.Executor.Type) {
		return registry.Need{}, false, nil
	}
	for _, d := range m.Rights {
		agents := d.Right == manifest.Network && d.Whom() == manifest.Agents
		if r, _ := manifest.RightOf(d.Right); !r.Exit || cmd.Executor.Type == "agent" && !agents || cmd.Executor.Type != "agent" && agents {
			continue
		}
		if held := m.Held(granted, d.Right, d.Who); len(held) > 0 {
			return registry.Need{Right: d.Right, Who: d.Whom(), Subject: held[0]}, true, nil
		}
	}
	return registry.Need{}, false, nil
}

// marked says the turn that makes a call now read untrusted text and holds something of value.
func (g *Gateway) marked(ctx context.Context, act registry.Actor) bool {
	p, live := g.turn(ctx, act)
	return live && Marked(p)
}

// Marked says a turn read untrusted text and holds something of value, so decide lets its ways out go by the owner's
// once.
func Marked(p db.Process) bool { return p.ReadUntrusted != "" && p.HoldsValued == 1 }

// brought marks the process of act by what the call of c gave it: an answer from outside always, the data of a
// module, process or topic when theirs holds untrusted text.
func (g *Gateway) brought(ctx context.Context, act registry.Actor, c *registry.Command, project int64, input json.RawMessage) {
	if act.ProcessID == 0 || act.Kind == registry.Owner || c.Gate.Brings == "" || c.Gate.Brings == registry.FromDeck {
		return
	}
	from := ""
	switch c.Gate.Brings {
	case registry.FromOutside:
		from = c.Name + " " + untrusted.Cut(Canonical(input), 200)
	case registry.FromData:
		from = g.dataFrom(ctx, act, c, project, input)
	}
	if from == "" {
		return
	}
	err := g.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		return q.MarkProcessRead(ctx, db.MarkProcessReadParams{ReadUntrusted: from, ID: act.ProcessID})
	})
	if err != nil {
		slog.Error("mark a process", "process", act.ProcessID, "err", err)
	}
}

// taint marks the data of the module a call of c writes as holding untrusted text when its caller is a process that
// read some, or an automation token the owner does not vouch for; a call the owner's answer let through marks nothing.
func (g *Gateway) taint(ctx context.Context, act registry.Actor, c *registry.Command, project int64, input json.RawMessage, v Verdict) {
	if c.Readable || act.Kind == registry.Owner || v.Name == "cleared" {
		return
	}
	from := ""
	switch {
	case act.Token != 0:
		t, err := g.st.Q.GetToken(ctx, act.Token)
		if err != nil || t.Trusted == 1 {
			return
		}
		from = fmt.Sprintf("automation token %s", t.Name)
	case act.ProcessID != 0:
		p, err := g.st.Q.GetProcess(ctx, act.ProcessID)
		if err != nil || p.ReadUntrusted == "" {
			return
		}
		from = fmt.Sprintf("process %d, which read %s", p.ID, p.ReadUntrusted)
	default:
		return
	}
	targets := g.written(ctx, act.Workspace, c, project, input)
	err := g.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		for _, id := range targets {
			if err := q.MarkProjectUntrusted(ctx, db.MarkProjectUntrustedParams{Untrusted: untrusted.Cut(fmt.Sprintf("the call %s of %s", c.Name, from), 300), ID: id}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("mark the data of a module", "command", c.Name, "err", err)
	}
}

// written are the modules whose data a call of c writes: those its input names or of the processes it names, else its
// module, else the workspace, whose mark project master holds with the owner's memory.
func (g *Gateway) written(ctx context.Context, workspace int64, c *registry.Command, project int64, input json.RawMessage) []int64 {
	n := g.named(ctx, workspace, c, input)
	for _, id := range n.processes {
		if p, err := g.st.Q.GetProcess(ctx, id); err == nil {
			n.projects = append(n.projects, p.ProjectID)
		}
	}
	if len(n.projects) > 0 {
		return n.projects
	}
	if project != 0 {
		return []int64{project}
	}
	m, err := g.st.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: master.Name})
	if err != nil || m.Builtin == 0 {
		return nil
	}
	return []int64{m.ID}
}

// dataFrom is where the untrusted text of the data a call reads came from, empty for none: of what its input names,
// else of its module, else, read across modules, of any module of the workspace.
func (g *Gateway) dataFrom(ctx context.Context, act registry.Actor, c *registry.Command, project int64, input json.RawMessage) string {
	n := g.named(ctx, act.Workspace, c, input)
	for _, id := range n.processes {
		if p, err := g.st.Q.GetProcess(ctx, id); err == nil && p.ReadUntrusted != "" {
			return fmt.Sprintf("process %d, which read %s", p.ID, p.ReadUntrusted)
		}
	}
	if n.topic != 0 {
		if t, err := g.st.Q.TopicRead(ctx, n.topic); err == nil {
			return fmt.Sprintf("topic %d: process %d, which read %s", n.topic, t.ID, t.ReadUntrusted)
		}
	}
	if len(n.projects) == 0 && project != 0 && (c.ProjectID != 0 || act.Kind == registry.Agent) {
		n.projects = []int64{project}
	}
	for _, id := range n.projects {
		if p, err := g.st.Q.GetProject(ctx, id); err == nil && p.Untrusted != "" {
			return fmt.Sprintf("the data of %s, written by %s", p.Name, p.Untrusted)
		}
	}
	if len(n.projects) > 0 || len(n.processes) > 0 || n.topic != 0 {
		return ""
	}
	p, err := g.st.Q.FirstUntrustedProject(ctx, act.Workspace)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return ""
	}
	return fmt.Sprintf("the data of %s, written by %s", p.Name, p.Untrusted)
}

// names are the modules, processes and topic the input of a call names by the formats of its schema.
type names struct {
	projects, processes []int64
	topic               int64
}

// named are what the input of a call of c names: a project by name, a process, a record or a document by id, a topic.
func (g *Gateway) named(ctx context.Context, workspace int64, c *registry.Command, input json.RawMessage) names {
	var schema struct {
		Properties map[string]struct {
			Format string `json:"format"`
		} `json:"properties"`
	}
	var in map[string]json.RawMessage
	var out names
	if json.Unmarshal(c.Input, &schema) != nil || json.Unmarshal(input, &in) != nil {
		return out
	}
	for field, raw := range in {
		format := schema.Properties[field].Format
		if field == "topic" {
			format = "topic"
		}
		var name string
		_ = json.Unmarshal(raw, &name)
		id, _ := strconv.ParseInt(name, 10, 64)
		if id == 0 {
			_ = json.Unmarshal(raw, &id)
		}
		switch format {
		case "project":
			if p, err := g.st.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: name}); err == nil {
				out.projects = append(out.projects, p.ID)
			}
		case "process":
			if id != 0 {
				out.processes = append(out.processes, id)
			}
		case "record":
			if r, err := g.st.Q.GetRecord(ctx, id); err == nil {
				out.projects = append(out.projects, r.ProjectID)
			}
		case "document":
			if d, err := g.st.Q.GetDocument(ctx, id); err == nil && d.ProjectID.Valid {
				out.projects = append(out.projects, d.ProjectID.Int64)
			}
		case "topic":
			out.topic = id
		}
	}
	return out
}
