package scenarios

import (
	deckhome "deck2/internal/home"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider/codex"
)

// fakeCodex is a folder with the fake Codex app-server and the streams it replays.
type fakeCodex struct{ fakeBin }

func newCodex(t *testing.T) *fakeCodex {
	t.Helper()
	dir := t.TempDir()
	self, _ := os.Executable()
	root, _ := filepath.Abs(".")
	script := "#!/bin/sh\nexec env DECK_FAKE_CODEX='" + dir + "' DECK_FAKE_CODEX_ROOT='" + root + "' '" + self + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &fakeCodex{fakeBin{t: t, dir: dir, fixtures: "testdata/codex", version: "CLI", who: "the app-server"}}
}

func (c *fakeCodex) adapter(home string) *codex.Adapter {
	return &codex.Adapter{Binary: filepath.Join(c.dir, "codex"), Home: home, Dir: filepath.Join(deckhome.Layout(home).State(), "codex"), Linger: 300 * time.Millisecond}
}

// codexEnv runs deck on a clock a few minutes before the limit reset the recorded streams carry.
func codexEnv(t *testing.T, c *fakeCodex) (*Env, string) {
	home := t.TempDir()
	shift := time.Until(time.Unix(1790481864-600, 0))
	return newEnvWith(t, home, c.adapter(home), func(cfg *app.Config) { cfg.Now = func() time.Time { return time.Now().Add(shift) } }), home
}

type codexCall struct {
	args      []string
	env       map[string]string
	cwd       string
	rpc       []map[string]json.RawMessage
	answers   []map[string]json.RawMessage
	mcp       []map[string]any
	violation string
}

func (c *fakeCodex) call(n int) codexCall {
	c.t.Helper()
	read := c.files(n)
	cc := codexCall{env: map[string]string{}, cwd: read("cwd"), violation: read("violation"), args: strings.Split(read("args"), "\n")}
	for _, l := range strings.Split(read("env"), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			cc.env[k] = v
		}
	}
	cc.rpc = jsonLines[map[string]json.RawMessage](read("rpc.jsonl"))
	cc.answers = jsonLines[map[string]json.RawMessage](read("answers.jsonl"))
	cc.mcp = jsonLines[map[string]any](read("mcp.jsonl"))
	return cc
}

// request is the params of the first request of a method deck sent in the call.
func (cc codexCall) request(method string) map[string]json.RawMessage {
	for _, m := range cc.rpc {
		if string(m["method"]) == `"`+method+`"` && len(m["id"]) > 0 {
			var p map[string]json.RawMessage
			_ = json.Unmarshal(m["params"], &p)
			return p
		}
	}
	return nil
}

// config is the value of a -c argument deck started the app-server with.
func (cc codexCall) config(key string) (string, bool) {
	for i, a := range cc.args {
		if i > 0 && cc.args[i-1] == "-c" && strings.HasPrefix(a, key+"=") {
			return strings.TrimPrefix(a, key+"="), true
		}
	}
	return "", false
}
