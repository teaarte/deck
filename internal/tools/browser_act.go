package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"deck2/internal/gateway"
	"deck2/internal/registry"
)

// actor makes an action on the page the browser shows now: on an act domain, on a page the owner allowed
// this call on, or for a tool the project lists; files it sends come only from the process's own folder.
func (b *Browser) actor(tool string) registry.Handler {
	name := registry.Builtin + "." + tool
	return func(ctx context.Context, act registry.Actor, raw json.RawMessage) (any, error) {
		m, err := b.Manifests(ctx, act.ProjectID)
		if err != nil {
			return nil, err
		}
		args, err := b.ownPaths(act.ProcessID, raw)
		if err != nil {
			b.GW.Refused(ctx, act, act.ProjectID, name, raw, err)
			return nil, err
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		page, err := b.current(ctx)
		approved, yes := gateway.Approved(ctx)
		listed := slices.Contains(m.AllowedExternal, name)
		if err == nil && !on(m.Domains.Act, page) && !(yes && approved == page) && !listed {
			err = registry.Refuse("deck: the browser shows %s now, which is not on an act domain; look at the page again and repeat the call", page)
		}
		if err != nil {
			b.GW.Refused(ctx, act, act.ProjectID, name, raw, err)
			return nil, err
		}
		intent, err := b.GW.Intent(ctx, act, act.ProjectID, name, raw)
		if err != nil {
			b.GW.Refused(ctx, act, act.ProjectID, name, raw, err)
			return nil, err
		}
		res, err := b.call(ctx, tool, args)
		if err != nil {
			return nil, failed(ctx, b.GW, intent, name, err)
		}
		out, cerr := Content(b.Dirs, act.ProcessID, name, res)
		text := ""
		if cerr != nil {
			text = cerr.Error()
		}
		b.GW.Finish(ctx, intent, "done", page, text)
		return out, cerr
	}
}

// ownPaths keeps the files an action sends inside the run folder of the process; results/<name> is short
// for a file of its results folder.
func (b *Browser) ownPaths(process int64, raw json.RawMessage) (json.RawMessage, error) {
	var args map[string]any
	if json.Unmarshal(raw, &args) != nil || args["paths"] == nil {
		return raw, nil
	}
	list, _ := args["paths"].([]any)
	root := filepath.Join(b.Dirs.Runs, strconv.FormatInt(process, 10))
	base, err := filepath.EvalSymlinks(root)
	if process == 0 || err != nil {
		return nil, registry.Refuse("deck: only a process sends files, from its own folder")
	}
	out := make([]any, 0, len(list))
	for _, p := range list {
		s, _ := p.(string)
		if !filepath.IsAbs(s) {
			s = filepath.Join(root, s)
		}
		real, err := filepath.EvalSymlinks(s)
		if err != nil || !strings.HasPrefix(real, base+string(filepath.Separator)) {
			return nil, registry.Refuse("deck: %q is not a file in the folder of this process", p)
		}
		out = append(out, real)
	}
	args["paths"] = out
	return json.Marshal(args)
}
