package screens

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
)

// commandOf is a command with its project and the command of the manifest behind it, nil for deck's own.
type commandOf struct {
	*registry.Command
	project string
	short   string
	of      *manifest.Command
}

// lookup finds a command the way every reader of what a command takes does.
func (s *Service) lookup(ctx context.Context, workspace int64, name string) (commandOf, error) {
	c, err := s.Registry.Lookup(ctx, workspace, name)
	if err != nil {
		return commandOf{}, err
	}
	out := commandOf{Command: c}
	if c.ProjectID == 0 {
		return out, nil
	}
	p, err := s.St.Q.GetProject(ctx, c.ProjectID)
	if err != nil {
		return out, nil
	}
	out.project, out.short = p.Name, strings.TrimPrefix(c.Name, p.Name+".")
	if m, _ := docs.Manifest(ctx, s.St.Q, p); m != nil {
		out.of = m.Command(out.short)
	}
	return out, nil
}

// Command is a command as a form shows it: its fields in the order the manifest writes them, without the fields
// only the registry adds.
func (s screenSources) Command(ctx context.Context, workspace int64, name string) (map[string]any, error) {
	c, err := s.svc.lookup(ctx, workspace, name)
	if err != nil {
		return nil, err
	}
	var in map[string]any
	_ = json.Unmarshal(c.Input, &in)
	if props, ok := in["properties"].(map[string]any); ok {
		for k := range props {
			if registry.Reserved(k) {
				delete(props, k)
			}
		}
	}
	raw := c.Input
	if c.of != nil {
		raw = c.of.InputSchema()
	}
	order := slices.DeleteFunc(manifest.PropertyOrder(raw), registry.Reserved)
	out := map[string]any{"name": c.Name, "title": c.Title, "description": c.Description, "input": in, "order": order}
	if c.project != "" {
		out["project"] = c.project
	}
	if len(c.Result) > 0 {
		out["result"] = c.Result
	}
	return out, nil
}
