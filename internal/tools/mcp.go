package tools

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/boxes"
	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/home"
	"deck2/internal/manifest"
	"deck2/internal/mcphost"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// MCP turns the tools of project MCP servers into registry commands; deck starts the servers itself.
type MCP struct {
	GW      *gateway.Gateway
	Host    *mcphost.Host
	Store   *store.Store
	Secrets func(workspace int64, project, name string) (string, error)
	Home    home.Layout
}

// ServerKey names a server of a project in the host.
func ServerKey(project int64, server string) string { return fmt.Sprintf("%d/%s", project, server) }

// ParseKey splits a host key back into project and server; ok is false for servers that are not a project's.
func ParseKey(key string) (int64, string, bool) {
	id, server, ok := strings.Cut(key, "/")
	n, err := strconv.ParseInt(id, 10, 64)
	return n, server, ok && err == nil
}

// SpecID identifies a server as the manifest declares it; a new one starts the rights of its tools over.
func SpecID(s manifest.MCPServer) string {
	b, _ := json.Marshal([]any{s.Command, s.Args, s.Env, s.URL})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func (x *MCP) spec(ctx context.Context, p db.Project, name string, s manifest.MCPServer, m *manifest.Manifest) (mcphost.Spec, error) {
	g, err := docs.Grants(ctx, x.Store.Q, p.ID)
	if err != nil {
		return mcphost.Spec{}, err
	}
	if s.URL != "" {
		return mcphost.Spec{URL: s.URL, Domains: m.Held(g, manifest.Network, manifest.Programs)}, nil
	}
	dir := x.Home.MCP(p.ID, name)
	var env, secrets []string
	for _, k := range slices.Sorted(maps.Keys(s.Env)) {
		v, used, err := x.expand(p, s.Env[k])
		if err != nil {
			return mcphost.Spec{}, err
		}
		env, secrets = append(env, k+"="+v), append(secrets, used...)
	}
	folders, err := x.Store.Q.ListMachineFolders(ctx, db.ListMachineFoldersParams{ProjectID: p.ID})
	if err != nil {
		return mcphost.Spec{}, err
	}
	var paths []string
	for _, f := range folders {
		paths = append(paths, f.Path)
	}
	b := boxes.For(sandbox.KindMCP, m, g, false, paths)
	b.Mark = x.Host.Sandbox.Mark(sandbox.KindMCP, ServerKey(p.ID, name))
	b.Argv, b.Dir, b.Home, b.Env, b.Write = append([]string{s.Command}, s.Args...), dir, dir, env, []string{dir}
	return mcphost.Spec{Box: b, Secrets: secrets}, nil
}

func (x *MCP) expand(p db.Project, v string) (string, []string, error) {
	var serr error
	var used []string
	out := manifest.SecretRef.ReplaceAllStringFunc(v, func(m string) string {
		s, err := x.Secrets(p.WorkspaceID, p.Name, strings.TrimPrefix(m, manifest.SecretPrefix))
		if err != nil && serr == nil {
			serr = err
		}
		used = append(used, s)
		return s
	})
	return out, used, serr
}

// Start starts the servers of an enabled project whose tools deck has not listed under their spec yet, and stops a
// running one whose spec changed, such as its domains.
func (x *MCP) Start(ctx context.Context, p db.Project, m *manifest.Manifest) {
	if p.Status != store.ProjectEnabled || len(m.Tools.MCP.Servers) == 0 {
		return
	}
	rows, err := x.Store.Q.ListMCPTools(ctx, p.ID)
	if err != nil {
		return
	}
	for _, name := range slices.Sorted(maps.Keys(m.Tools.MCP.Servers)) {
		s := m.Tools.MCP.Servers[name]
		spec, err := x.spec(ctx, p, name, s, m)
		if err != nil {
			if x.Host.Status != nil {
				x.Host.Status(ServerKey(p.ID, name), nil, fmt.Errorf("MCP server %s did not start: %w", name, err))
			}
			continue
		}
		if slices.ContainsFunc(rows, func(r db.McpTool) bool { return r.Server == name && r.Spec == SpecID(s) }) {
			x.Host.Retire(ServerKey(p.ID, name), spec)
			continue
		}
		_, _ = x.Host.Tools(ctx, ServerKey(p.ID, name), spec)
	}
}

// Keep stores the tools a server listed after it started; a tool whose name, description or input changed has
// another print, so an answer of always to the old one no longer holds.
func (x *MCP) Keep(ctx context.Context, project int64, server string, tools []*mcp.Tool) error {
	p, err := x.Store.Q.GetProject(ctx, project)
	if err != nil {
		return err
	}
	m, err := docs.Manifest(ctx, x.Store.Q, p)
	if err != nil {
		return err
	}
	s, ok := m.Tools.MCP.Servers[server]
	if !ok {
		return nil
	}
	keep := []string{}
	return x.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		for _, t := range tools {
			input, _ := json.Marshal(t.InputSchema)
			print := toolPrint(t.Name, t.Description, input)
			keep = append(keep, t.Name)
			if err := q.UpsertMCPTool(ctx, db.UpsertMCPToolParams{ProjectID: project, Server: server, Spec: SpecID(s), Name: t.Name,
				Title: t.Title, Description: t.Description, Input: string(input), Fingerprint: print}); err != nil {
				return err
			}
		}
		b, _ := json.Marshal(keep)
		return q.UnlistMCPToolsExcept(ctx, db.UnlistMCPToolsExceptParams{ProjectID: project, Server: server, Keep: string(b)})
	})
}

func toolPrint(name, description string, input []byte) string {
	sum := sha256.Sum256([]byte(name + "\x00" + description + "\x00" + gateway.Canonical(input)))
	return hex.EncodeToString(sum[:12])
}

// Commands are the tools deck listed of a project's servers under their spec, through toolFilter; a call needs the
// right its server names, send by default, on server.tool at its print, and none beyond the server's for network.
func (x *MCP) Commands(ctx context.Context, q *db.Queries, p db.Project, m *manifest.Manifest) ([]*registry.Command, error) {
	if len(m.Tools.MCP.Servers) == 0 {
		return nil, nil
	}
	rows, err := q.ListMCPTools(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	var out []*registry.Command
	for _, r := range rows {
		s, ok := m.Tools.MCP.Servers[r.Server]
		if !ok || r.Listed == 0 || r.Spec != SpecID(s) || len(s.ToolFilter) > 0 && !slices.Contains(s.ToolFilter, r.Name) {
			continue
		}
		right := manifest.ServerRight(s, r.Name)
		print := r.Spec + "/" + r.Fingerprint
		need := registry.Need{Right: right, Subject: r.Server + "." + r.Name, Print: print}
		c := registry.Loose(p.Name+"."+r.Server+"."+r.Name, cmp.Or(r.Title, r.Name), cmp.Or(r.Description, r.Name), registry.Everyone, json.RawMessage(r.Input),
			x.handler(p, r.Server, s, m, r.Name, right == manifest.Send, r.Fingerprint)).Foreign()
		c.ProjectID = p.ID
		if right == manifest.Network {
			// A tool that only reaches the hosts the box of the server holds stays in deck's sight.
			c.Stays()
		} else {
			c.Gate = registry.Gate{
				Subject: func(context.Context, registry.At, json.RawMessage) registry.Subject {
					return registry.Subject{Print: print, Where: "server " + r.Server + ", tool " + r.Name}
				},
				Needs: func(context.Context, registry.Actor, registry.Subject, json.RawMessage) ([]registry.Need, error) {
					return []registry.Need{need}, nil
				}}
		}
		out = append(out, c)
	}
	return out, nil
}

func (x *MCP) handler(p db.Project, server string, s manifest.MCPServer, m *manifest.Manifest, tool string, external bool, print string) registry.Handler {
	name := p.Name + "." + server + "." + tool
	frozen := SpecID(s) + "/" + print
	same := func(list []*mcp.Tool) error {
		for _, t := range list {
			if t.Name == tool {
				input, _ := json.Marshal(t.InputSchema)
				if toolPrint(t.Name, t.Description, input) == print {
					return nil
				}
			}
		}
		return registry.Refuse("%s changed since this call was checked; it was not made, call it again", name)
	}
	return func(ctx context.Context, act registry.Actor, raw json.RawMessage) (any, error) {
		spec, err := x.spec(ctx, p, server, s, m)
		if err == nil && external {
			var intent int64
			if intent, err = x.GW.Intent(ctx, act, p.ID, name, raw); err == nil {
				return x.call(ctx, act, name, ServerKey(p.ID, server), spec, tool, raw, intent, frozen, same)
			}
		}
		if err != nil {
			if external {
				x.GW.Refused(ctx, act, p.ID, name, raw, err)
			}
			return nil, err
		}
		return x.call(ctx, act, name, ServerKey(p.ID, server), spec, tool, raw, 0, frozen, same)
	}
}

// call makes a tool call and settles its intent: rejected when it never reached the server, done when the
// server answered, unknown when the connection was lost while the call was out.
func (x *MCP) call(ctx context.Context, act registry.Actor, name, key string, spec mcphost.Spec, tool string, raw json.RawMessage, intent int64,
	print string, check func([]*mcp.Tool) error) (any, error) {
	res, err := x.Host.Call(ctx, key, spec, tool, raw, check)
	if err != nil {
		return nil, failed(ctx, x.GW, intent, name, print, err, spec.Secrets...)
	}
	out, cerr := Content(x.Home, act.ProcessID, name, res)
	out, cerr = redacted(out, spec.Secrets), redactErr(cerr, spec.Secrets)
	done(ctx, x.GW, intent, "", cerr)
	return out, cerr
}

// redacted is a tool result without the secret values deck gave the tool.
func redacted(v any, secrets []string) any {
	if v == nil || len(secrets) == 0 {
		return v
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	clean := untrusted.Redact(string(b), secrets)
	if clean == string(b) {
		return v
	}
	var out any
	if json.Unmarshal([]byte(clean), &out) != nil {
		return map[string]any{"text": clean}
	}
	return out
}

func redactErr(err error, secrets []string) error {
	if err == nil || len(secrets) == 0 {
		return err
	}
	if clean := untrusted.Redact(err.Error(), secrets); clean != err.Error() {
		return errors.New(clean)
	}
	return err
}
