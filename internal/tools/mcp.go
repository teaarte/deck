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
	Secrets func(workspace int64, name string) (string, error)
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

// reached are the domains a server of a manifest reaches through deck's proxy: those it reads and acts on.
func reached(m *manifest.Manifest) []string {
	return m.Domains.All()
}

func (x *MCP) spec(ctx context.Context, p db.Project, name string, s manifest.MCPServer, domains []string) (mcphost.Spec, error) {
	if s.URL != "" {
		return mcphost.Spec{URL: s.URL}, nil
	}
	dir := x.Home.MCP(p.ID, name)
	env := sandbox.Env(dir, dir)
	var secrets []string
	for _, k := range slices.Sorted(maps.Keys(s.Env)) {
		v, used, err := x.expand(p.WorkspaceID, s.Env[k])
		if err != nil {
			return mcphost.Spec{}, err
		}
		env, secrets = append(env, k+"="+v), append(secrets, used...)
	}
	folders, err := x.Store.Q.ListProjectFolders(ctx, p.ID)
	if err != nil {
		return mcphost.Spec{}, err
	}
	var read []string
	for _, f := range folders {
		read = append(read, f.Path)
	}
	return mcphost.Spec{Command: s.Command, Args: s.Args, Env: env, Dir: dir, Read: read, Confine: true, Domains: domains, Secrets: secrets}, nil
}

func (x *MCP) expand(workspace int64, v string) (string, []string, error) {
	var serr error
	var used []string
	out := manifest.SecretRef.ReplaceAllStringFunc(v, func(m string) string {
		s, err := x.Secrets(workspace, strings.TrimPrefix(m, manifest.SecretPrefix))
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
		spec, err := x.spec(ctx, p, name, s, reached(m))
		if err != nil {
			continue
		}
		if slices.ContainsFunc(rows, func(r db.McpTool) bool { return r.Server == name && r.Spec == SpecID(s) }) {
			x.Host.Retire(ServerKey(p.ID, name), spec)
			continue
		}
		_, _ = x.Host.Tools(ctx, ServerKey(p.ID, name), spec)
	}
}

// Keep stores the tools a server listed after it started; a tool whose name, description or input changed
// since the owner's rights were given loses them until the owner allows it again.
func (x *MCP) Keep(ctx context.Context, project int64, server string, tools []*mcp.Tool) error {
	m, err := docs.For(ctx, x.Store.Q, registry.Actor{}, project)
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

// Commands are the registry commands of the tools of a project's servers that deck listed under their
// current spec, through toolFilter; a tool's class comes from classes, external when unlisted or changed.
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
		changed := r.Fingerprint != r.Approved
		class := s.Classes[r.Name]
		if class == "" || changed {
			class = registry.External
		}
		print := r.Spec + "/" + r.Fingerprint
		c := registry.Loose(p.Name+"."+r.Server+"."+r.Name, cmp.Or(r.Title, r.Name), cmp.Or(r.Description, r.Name), class, registry.Everyone, json.RawMessage(r.Input),
			x.handler(p, r.Server, s, reached(m), r.Name, class, r.Fingerprint)).Foreign()
		c.ProjectID = p.ID
		c.Gate = registry.Gate{Journals: class == registry.External,
			Fingerprint: func(context.Context, int64, int64, json.RawMessage) string { return print },
			Target:      func(string, json.RawMessage) string { return "server " + r.Server + ", tool " + r.Name },
			Allowed: func(_ context.Context, _ registry.Actor, _ json.RawMessage, listed bool) (bool, error) {
				return listed && !changed, nil
			}}
		out = append(out, c)
	}
	return out, nil
}

func (x *MCP) handler(p db.Project, server string, s manifest.MCPServer, domains []string, tool, class, print string) registry.Handler {
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
		external := class == registry.External
		spec, err := x.spec(ctx, p, server, s, domains)
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
