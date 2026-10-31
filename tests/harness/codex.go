package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/provider/codex"
)

// fakeCodex is a folder with the fake Codex app-server and the streams it replays.
type fakeCodex struct{ fakeBin }

func NewCodex(t *testing.T) *fakeCodex {
	t.Helper()
	dir := t.TempDir()
	self := Fakes
	script := "#!/bin/sh\nexec env DECK_FAKE_CODEX='" + dir + "' '" + self + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &fakeCodex{fakeBin{T: t, Dir: dir, fixtures: Testdata("codex"), version: "CLI", who: "the app-server"}}
}

func (c *fakeCodex) Adapter(home string) *codex.Adapter {
	return &codex.Adapter{Binary: filepath.Join(c.Dir, "codex"), Home: deckhome.Layout(home), Dir: deckhome.Layout(home).Codex(), Linger: 300 * time.Millisecond}
}

// WithCodex runs deck on the fake Codex app-server, on a clock a few minutes before the limit reset the recorded
// streams carry.
func WithCodex(c *fakeCodex) Option {
	shift := time.Until(time.Unix(1790481864-600, 0))
	return func(s *setup) {
		s.providers = append(s.providers, func(home string) provider.Provider { return c.Adapter(home) })
		s.cfg = append(s.cfg, func(cfg *app.Config) { cfg.Now = func() time.Time { return time.Now().Add(shift) } })
	}
}

type codexCall struct {
	Args      []string
	Env       map[string]string
	Cwd       string
	rpc       []map[string]json.RawMessage
	Answers   []map[string]json.RawMessage
	Mcp       []map[string]any
	Violation string
}

func (c *fakeCodex) Call(n int) codexCall {
	c.T.Helper()
	read := c.Files(n)
	cc := codexCall{Env: map[string]string{}, Cwd: read("cwd"), Violation: read("violation"), Args: strings.Split(read("args"), "\n")}
	for _, l := range strings.Split(read("env"), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			cc.Env[k] = v
		}
	}
	cc.rpc = JsonLines[map[string]json.RawMessage](read("rpc.jsonl"))
	cc.Answers = JsonLines[map[string]json.RawMessage](read("answers.jsonl"))
	cc.Mcp = JsonLines[map[string]any](read("mcp.jsonl"))
	return cc
}

// Request is the params of the first request of a method deck sent in the call.
func (cc codexCall) Request(method string) map[string]json.RawMessage {
	for _, m := range cc.rpc {
		if string(m["method"]) == `"`+method+`"` && len(m["id"]) > 0 {
			var p map[string]json.RawMessage
			_ = json.Unmarshal(m["params"], &p)
			return p
		}
	}
	return nil
}

// Config is the value of a -c argument deck started the app-server with.
func (cc codexCall) Config(key string) (string, bool) {
	for i, a := range cc.Args {
		if i > 0 && cc.Args[i-1] == "-c" && strings.HasPrefix(a, key+"=") {
			return strings.TrimPrefix(a, key+"="), true
		}
	}
	return "", false
}

func QuoteTOML(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
