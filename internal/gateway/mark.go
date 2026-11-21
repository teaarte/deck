package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
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

// taints says a call of act marks the data it writes: of a process that read untrusted text, or of a token from
// outside deck.
func (g *Gateway) taints(ctx context.Context, act registry.Actor) bool {
	if act.Token != 0 {
		return act.Outside
	}
	if act.ProcessID == 0 {
		return false
	}
	p, err := g.st.Q.GetProcess(ctx, act.ProcessID)
	return err == nil && p.ReadUntrusted != ""
}

// Marked says a turn read untrusted text and holds something of value, so decide lets its ways out go by the owner's
// once.
func Marked(p db.Process) bool { return p.ReadUntrusted != "" && p.HoldsValued == 1 }

// brought is what the call of c gives the process of act that marks it, in words and as data, empty for nothing: an
// answer from outside always, the data of a module, process or topic when theirs holds untrusted text.
func (g *Gateway) brought(ctx context.Context, act registry.Actor, c *registry.Command, project int64, input json.RawMessage) (string, untrusted.From) {
	if act.ProcessID == 0 || act.Kind == registry.Owner || c.Gate.Brings == "" || c.Gate.Brings == registry.FromDeck {
		return "", untrusted.From{}
	}
	switch c.Gate.Brings {
	case registry.FromOutside:
		return c.Name + " " + untrusted.Cut(Canonical(input), 200), outside(c.Name, input)
	case registry.FromData:
		return g.dataFrom(ctx, act, c, project, input)
	}
	return "", untrusted.From{}
}

// outside is the first source of an answer from outside of a call of command with input: a page by its host, a
// search, else a tool by its name.
func outside(command string, input json.RawMessage) untrusted.From {
	switch command {
	case registry.Deck("web_fetch"):
		var in struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(input, &in)
		host := ""
		if u, err := url.Parse(in.URL); err == nil {
			host = u.Hostname()
		}
		return untrusted.Of(untrusted.WebFetch, host)
	case registry.Deck("web_search"):
		return untrusted.Of(untrusted.WebSearch, "")
	}
	return untrusted.Of(untrusted.Tool, command)
}

// fromOf is the From stored as s, nil for a mark written before deck kept it.
func fromOf(s string) *untrusted.From {
	if f := untrusted.FromOf(s); f.Source.Kind != "" {
		return &f
	}
	return nil
}

// taint is the mark a call of c puts on the data of the modules it writes, with them: by a process that read untrusted
// text or a token the owner does not vouch for; none for a call the owner's answer or its command's right let through.
func (g *Gateway) taint(ctx context.Context, act registry.Actor, c *registry.Command, project int64, input json.RawMessage, v Verdict, answered bool) (string, untrusted.From, []int64) {
	if c.Readable || act.Kind == registry.Owner || v.Name == "cleared" && (answered || v.Facts.Needs) {
		return "", untrusted.From{}, nil
	}
	from, data := "", untrusted.From{}
	switch {
	case act.Token != 0:
		if !act.Outside {
			return "", data, nil
		}
		t, err := g.st.Q.GetToken(ctx, act.Token)
		if err != nil {
			return "", data, nil
		}
		from, data = fmt.Sprintf("automation token %s", t.Name), untrusted.Of(untrusted.Token, t.Name)
	case act.ProcessID != 0:
		p, err := g.st.Q.GetProcess(ctx, act.ProcessID)
		if err != nil || p.ReadUntrusted == "" {
			return "", data, nil
		}
		from, data = fmt.Sprintf("process %d, which read %s", p.ID, p.ReadUntrusted), untrusted.FromOf(p.ReadFrom)
	default:
		return "", data, nil
	}
	targets := g.written(ctx, act.Workspace, c, project, input)
	if act.Kind == registry.Agent {
		// An agent writes no module but its own: a call naming another is refused, and marks nothing.
		targets = slices.DeleteFunc(targets, func(id int64) bool { return !act.Within(id) })
	}
	return untrusted.Cut(fmt.Sprintf("the call %s of %s", c.Name, from), 300), data, targets
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

// dataFrom is where the untrusted text of the data a call reads came from, in words and as data, empty for none: of
// what its input names, else of its module, else, read across modules, of any module of the workspace.
func (g *Gateway) dataFrom(ctx context.Context, act registry.Actor, c *registry.Command, project int64, input json.RawMessage) (string, untrusted.From) {
	n := g.named(ctx, act.Workspace, c, input)
	for _, id := range n.processes {
		if p, err := g.st.Q.GetProcess(ctx, id); err == nil && p.ReadUntrusted != "" {
			return fmt.Sprintf("process %d, which read %s", p.ID, p.ReadUntrusted), untrusted.FromOf(p.ReadFrom)
		}
	}
	if n.topic != 0 {
		if t, err := g.st.Q.TopicRead(ctx, n.topic); err == nil {
			return fmt.Sprintf("topic %d: process %d, which read %s", n.topic, t.ID, t.ReadUntrusted), untrusted.FromOf(t.ReadFrom)
		}
	}
	if len(n.projects) == 0 && project != 0 && (c.ProjectID != 0 || act.Kind == registry.Agent) {
		n.projects = []int64{project}
	}
	for _, id := range n.projects {
		if p, err := g.st.Q.GetProject(ctx, id); err == nil && p.Untrusted != "" {
			return fmt.Sprintf("the data of %s, written by %s", p.Name, p.Untrusted), untrusted.FromOf(p.ReadFrom).Through(untrusted.Data, p.Name)
		}
	}
	if len(n.projects) > 0 || len(n.processes) > 0 || n.topic != 0 {
		return "", untrusted.From{}
	}
	p, err := g.st.Q.FirstUntrustedProject(ctx, act.Workspace)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return "", untrusted.From{}
	}
	return fmt.Sprintf("the data of %s, written by %s", p.Name, p.Untrusted), untrusted.FromOf(p.ReadFrom).Through(untrusted.Data, p.Name)
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
	for _, field := range slices.Sorted(maps.Keys(in)) {
		raw := in[field]
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

// Target is the module a call of command with input changes: that of a module's command, else the first project
// deck's command names in its input by field; empty for none, as a tool of the agent's provider.
func (g *Gateway) Target(ctx context.Context, workspace int64, command string, input json.RawMessage) string {
	c, err := g.reg.Lookup(ctx, workspace, command)
	if err != nil {
		return ""
	}
	var n names
	switch {
	case c.ProjectID != 0:
		// A module writes the schema of its own command, so the fields of its input name nothing deck vouches for.
		n.projects = []int64{c.ProjectID}
	case registry.IsBuiltin(c.Name):
		n = g.named(ctx, workspace, c, input)
		for _, id := range n.processes {
			if p, err := g.st.Q.GetProcess(ctx, id); err == nil {
				n.projects = append(n.projects, p.ProjectID)
			}
		}
	}
	for _, id := range n.projects {
		if p, err := g.st.Q.GetProject(ctx, id); err == nil {
			return p.Name
		}
	}
	return ""
}
