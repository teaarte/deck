package harness

import (
	"context"
	"deck2/internal/provider"
	"testing"
)

// TwoModels is deck with the main fake and a backup one, the command work of plain assigned to both in that order.
func TwoModels(t *testing.T, main, backup func(context.Context, *Call) provider.Result, opts ...Option) (*Env, *Fake, int64) {
	t.Helper()
	m, b := &Fake{Script: main}, &Fake{ProviderName: "backup", Script: backup}
	e := New(t, append([]Option{WithProvider(m, b)}, opts...)...)
	p := e.Project("plain", TestManifest([]string{AgentCommand("work", `{"type": "object", "properties": {"n": {"type": "integer"}}}`)}, nil))
	e.UseModels("plain.work", "fake", "backup")
	return e, b, p
}

func (e *Env) StartWork(n int) int64 {
	e.T.Helper()
	var r struct{ Process int64 }
	e.Must("plain.work", map[string]any{"n": n}, &r)
	return r.Process
}

// Following waits for the one process that goes on with the work of id.
func (e *Env) Following(id int64) int64 {
	e.T.Helper()
	var next int64
	e.Until("the process that goes on", func() bool {
		if got := e.Fault(id); len(got.FollowedBy) == 1 {
			next = got.FollowedBy[0]
		}
		return next != 0
	})
	return next
}
