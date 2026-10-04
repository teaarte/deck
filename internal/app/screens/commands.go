package screens

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"deck2/internal/manifest"
	"deck2/internal/registry"
)

// Command is a command as a form shows it; the retry key deck adds to every command is not for the owner.
func (s Sources) Command(ctx context.Context, workspace int64, name string) (map[string]any, error) {
	c, err := s.Registry.Lookup(ctx, workspace, name)
	if err != nil {
		return nil, err
	}
	project := ""
	if c.ProjectID != 0 {
		if p, err := s.Store.Q.GetProject(ctx, c.ProjectID); err == nil {
			project = p.Name
		}
	}
	return s.commandForm(ctx, c, project), nil
}

// commandForm is a command as a form needs it; its fields keep the order the manifest writes them in.
func (s Sources) commandForm(ctx context.Context, c *registry.Command, project string) map[string]any {
	var in map[string]any
	_ = json.Unmarshal(c.Input, &in)
	if props, ok := in["properties"].(map[string]any); ok {
		delete(props, manifest.RetryKey)
	}
	raw := c.Input
	if c.ProjectID != 0 {
		if p, err := s.Store.Q.GetProject(ctx, c.ProjectID); err == nil {
			if m, err := s.Manifests.Of(ctx, p); err == nil {
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
