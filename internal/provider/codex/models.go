package codex

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"deck2/internal/provider"
)

// Models are the models the app-server lists, with the efforts each takes; no model is called.
func (a *Adapter) Models(ctx context.Context) ([]provider.Model, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.Command(a.Binary, "app-server")
	cmd.Env = environ()
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", a.Binary, err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	t := &turn{a: a, c: newConn(in, out)}
	go func() {
		<-ctx.Done()
		_ = cmd.Process.Kill()
	}()
	if err := t.call(ctx, "initialize", InitializeParams{ClientInfo: ClientInfo{Name: "deck", Title: "deck", Version: "0"}}, nil); err != nil {
		return nil, err
	}
	if err := t.c.notify("initialized", nil); err != nil {
		return nil, err
	}
	var list []provider.Model
	page := ModelListParams{}
	for {
		var r ModelListResponse
		if err := t.call(ctx, "model/list", page, &r); err != nil {
			return nil, err
		}
		for _, m := range r.Data {
			v := provider.Model{Names: []string{m.ID, m.Model}}
			for _, e := range m.SupportedReasoningEfforts {
				v.Efforts = append(v.Efforts, string(e.ReasoningEffort))
			}
			list = append(list, v)
		}
		if r.NextCursor == "" {
			return list, nil
		}
		page.Cursor = r.NextCursor
	}
}
