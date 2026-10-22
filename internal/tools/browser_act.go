package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"deck2/internal/docs"
	"deck2/internal/files"
	"deck2/internal/paths"
	"deck2/internal/untrusted"

	"deck2/internal/gateway"
	"deck2/internal/registry"
)

// actor makes an action on the page the browser shows now: on an act domain or on a page the owner allowed
// this call on; files it sends come only from the process's own folder.
func (b *Browser) actor(tool string) registry.Handler {
	name := registry.Builtin + "." + tool
	return func(ctx context.Context, act registry.Actor, raw json.RawMessage) (any, error) {
		m, err := docs.For(ctx, b.Store.Q, act, act.ProjectID)
		if err != nil {
			return nil, err
		}
		args, copies, err := b.ownPaths(act.ProcessID, raw)
		if copies != "" {
			defer os.RemoveAll(copies)
		}
		if err != nil {
			b.GW.Refused(ctx, act, act.ProjectID, name, raw, err)
			return nil, err
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		page, err := b.current(ctx, readsAnySite(m))
		approved, yes := gateway.Approved(ctx)
		if err == nil && !on(m.Domains.Act, page) && !(yes && page != "" && approved == page) {
			err = registry.Refuse("deck: the browser shows %s now, which is not on an act domain; look at the page again and repeat the call", untrusted.Line(page, 300))
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
		res, err := b.call(ctx, readsAnySite(m), tool, args)
		if err != nil {
			return nil, failed(ctx, b.GW, intent, name, page, err)
		}
		out, cerr := Content(b.Home, act.ProcessID, name, res)
		if now, off := b.offDomains(ctx, act.ProjectID); off && cerr == nil {
			out = map[string]any{"text": "Done. The browser now shows " + untrusted.Line(now, 300) + ", which is not on a read or act domain of this project, so what it shows is withheld."}
		}
		done(ctx, b.GW, intent, page, cerr)
		return out, cerr
	}
}

// ownPaths copies the files an action sends from the run folder of the process into the browser's own
// folder, the only one the browser server reads; results/<name> is short for a file of its results folder.
func (b *Browser) ownPaths(process int64, raw json.RawMessage) (json.RawMessage, string, error) {
	var args map[string]any
	if json.Unmarshal(raw, &args) != nil || args["paths"] == nil {
		return raw, "", nil
	}
	list, _ := args["paths"].([]any)
	runs := b.Home.Run(process)
	base, err := filepath.EvalSymlinks(runs)
	if process == 0 || err != nil {
		return nil, "", registry.Refuse("deck: only a process sends files, from its own folder")
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	if err := os.MkdirAll(b.dir("work"), 0o700); err != nil {
		return nil, "", err
	}
	dest, err := os.MkdirTemp(b.dir("work"), fmt.Sprintf("%d-", process))
	if err != nil {
		return nil, "", err
	}
	out := make([]any, 0, len(list))
	for _, p := range list {
		s, _ := p.(string)
		for _, dir := range paths.Names(runs) {
			if filepath.IsAbs(s) && paths.Within(dir, s) {
				s, _ = filepath.Rel(dir, s)
			}
		}
		real, err := files.Inside(base, s)
		if err != nil || filepath.IsAbs(s) {
			_ = os.RemoveAll(dest)
			return nil, "", registry.Refuse("deck: %q is not a file in the folder of this process", p)
		}
		rel, _ := filepath.Rel(base, real)
		sub := filepath.Join(dest, strconv.Itoa(len(out)))
		if err := os.Mkdir(sub, 0o700); err != nil {
			_ = os.RemoveAll(dest)
			return nil, "", err
		}
		copied, err := copyOut(root, rel, filepath.Join(sub, filepath.Base(rel)))
		if err != nil {
			_ = os.RemoveAll(dest)
			return nil, "", registry.Refuse("deck: %q is not a file in the folder of this process: %v", p, err)
		}
		out = append(out, copied)
	}
	args["paths"] = out
	b2, err := json.Marshal(args)
	return b2, dest, err
}

// copyOut copies a regular file of root, not following a link, to dst.
func copyOut(root *os.Root, rel, dst string) (string, error) {
	in, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	defer in.Close()
	if fi, err := in.Stat(); err != nil || !fi.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return "", err
	}
	return dst, out.Close()
}
