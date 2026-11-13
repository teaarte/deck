package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// turn is the process of an agent's turn or the master's that makes a call now, which deck marks by what it read; false
// for a program, a trigger, a token, the owner and a call made later for the caller.
func (g *Gateway) turn(ctx context.Context, act registry.Actor) (db.Process, bool) {
	if !Live(ctx) || act.ProcessID == 0 || act.Run != "" || act.Kind != registry.Agent && act.Kind != registry.Master {
		return db.Process{}, false
	}
	p, err := g.st.Q.GetProcess(ctx, act.ProcessID)
	return p, err == nil
}

// marked says the turn that makes a call now read untrusted text and holds something of value.
func (g *Gateway) marked(ctx context.Context, act registry.Actor) bool {
	p, live := g.turn(ctx, act)
	return live && Marked(p)
}

// Marked says a turn read untrusted text and holds something of value, so its ways out go only by the owner's once.
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
	n := g.named(ctx, act.Workspace, c, input)
	targets := n.projects
	if len(targets) == 0 && project != 0 {
		targets = []int64{project}
	}
	err := g.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		for _, id := range targets {
			if err := q.MarkProjectUntrusted(ctx, db.MarkProjectUntrustedParams{Untrusted: untrusted.Cut(c.Name+" by "+from, 300), ID: id}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("mark the data of a module", "command", c.Name, "err", err)
	}
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
			return fmt.Sprintf("the data of %s, where %s wrote", p.Name, p.Untrusted)
		}
	}
	if len(n.projects) > 0 || len(n.processes) > 0 || n.topic != 0 {
		return ""
	}
	p, err := g.st.Q.FirstUntrustedProject(ctx, act.Workspace)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return ""
	}
	return fmt.Sprintf("the data of %s, where %s wrote", p.Name, p.Untrusted)
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
