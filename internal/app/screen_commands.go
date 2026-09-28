package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/screen"
)

// Command is a command as a form shows it; the retry key deck adds to every command is not for the owner.
func (s sources) Command(ctx context.Context, workspace int64, name string) (map[string]any, error) {
	c, err := s.a.Registry.Lookup(ctx, workspace, name)
	if err != nil {
		return nil, err
	}
	return s.commandForm(ctx, c, ""), nil
}

// commandForm is a command as a form needs it; its fields keep the order the manifest writes them in.
func (s sources) commandForm(ctx context.Context, c *registry.Command, project string) map[string]any {
	var in map[string]any
	_ = json.Unmarshal(c.Input, &in)
	if props, ok := in["properties"].(map[string]any); ok {
		delete(props, manifest.RetryKey)
	}
	raw := c.Input
	if c.ProjectID != 0 {
		if p, err := s.a.Store.Q.GetProject(ctx, c.ProjectID); err == nil {
			if m, err := loadManifest(ctx, s.a.Store.Q, p); err == nil {
				if mc := m.Command(strings.TrimPrefix(c.Name, p.Name+".")); mc != nil {
					raw = mc.InputSchema()
				}
			}
		}
	}
	order := slices.DeleteFunc(manifest.PropertyOrder(raw), func(k string) bool { return k == manifest.RetryKey })
	out := map[string]any{"name": c.Name, "title": c.Title, "description": c.Description, "input": in, "order": order}
	if project != "" {
		out["project"] = project
	}
	if len(c.Result) > 0 {
		out["result"] = c.Result
	}
	return out
}

// commandItems are the commands the owner may call, of one project or all; files are their file fields.
func (s sources) commandItems(ctx context.Context, sc screen.Scope, names map[int64]string) ([]map[string]any, error) {
	cmds, err := s.a.Registry.List(ctx, registry.Actor{Kind: registry.Owner, Workspace: sc.Workspace})
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	manifests := map[int64]*manifest.Manifest{}
	for _, c := range cmds {
		if sc.Project != 0 && c.ProjectID != sc.Project {
			continue
		}
		executor := ""
		if c.ProjectID != 0 {
			m, ok := manifests[c.ProjectID]
			if !ok {
				if p, err := s.a.Store.Q.GetProject(ctx, c.ProjectID); err == nil {
					m, _ = loadManifest(ctx, s.a.Store.Q, p)
				}
				manifests[c.ProjectID] = m
			}
			if m != nil {
				if mc := m.Command(strings.TrimPrefix(c.Name, names[c.ProjectID]+".")); mc != nil {
					executor = mc.Executor.Type
				}
			}
		}
		f := s.commandForm(ctx, c, names[c.ProjectID])
		out = append(out, map[string]any{"id": c.Name, "name": c.Name, "title": c.Title, "description": c.Description,
			"project": names[c.ProjectID], "files": process.FileFields(c.Input), "executor": executor, "_command": f})
	}
	return out, nil
}
