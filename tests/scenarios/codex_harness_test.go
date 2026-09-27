package scenarios

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider/codex"
)

// fakeCodex is a folder with the fake Codex app-server and the streams it replays.
type fakeCodex struct {
	t   *testing.T
	dir string
}

func newCodex(t *testing.T) *fakeCodex {
	t.Helper()
	dir := t.TempDir()
	self, _ := os.Executable()
	root, _ := filepath.Abs(".")
	script := "#!/bin/sh\nexec env DECK_FAKE_CODEX='" + dir + "' DECK_FAKE_CODEX_ROOT='" + root + "' '" + self + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &fakeCodex{t: t, dir: dir}
}

// stream sets what call n replays; n = 0 sets it for every call without its own stream.
func (c *fakeCodex) stream(n int, fixture string) {
	var meta map[string]struct{ CLI, Date, Kind string }
	b, _ := os.ReadFile("testdata/codex/fixtures.json")
	if err := json.Unmarshal(b, &meta); err != nil {
		c.t.Fatal(err)
	}
	if m := meta[fixture]; m.CLI == "" || m.Date == "" || (m.Kind != "real" && m.Kind != "synthetic") {
		c.t.Fatalf("fixture %s needs its CLI version, date and kind in fixtures.json", fixture)
	}
	b, err := os.ReadFile(filepath.Join("testdata/codex", fixture))
	if err != nil {
		c.t.Fatal(err)
	}
	c.streamText(n, string(b))
}

func (c *fakeCodex) streamText(n int, text string) {
	name := "stream"
	if n > 0 {
		name += "." + strconv.Itoa(n)
	}
	if err := os.WriteFile(filepath.Join(c.dir, name), []byte(text), 0o644); err != nil {
		c.t.Fatal(err)
	}
}

func (c *fakeCodex) adapter(home string) *codex.Adapter {
	return &codex.Adapter{Binary: filepath.Join(c.dir, "codex"), Home: home, Dir: filepath.Join(home, "codex"), Linger: 300 * time.Millisecond}
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
	d := filepath.Join(c.dir, "calls", strconv.Itoa(n))
	if _, err := os.Stat(d); err != nil {
		c.t.Fatalf("the app-server was not started %d times", n)
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(d, name))
		return string(b)
	}
	cc := codexCall{env: map[string]string{}, cwd: read("cwd"), violation: read("violation"), args: strings.Split(read("args"), "\n")}
	for _, l := range strings.Split(read("env"), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			cc.env[k] = v
		}
	}
	lines := func(name string, out any) {
		for _, l := range strings.Split(strings.TrimSpace(read(name)), "\n") {
			if l == "" {
				continue
			}
			switch o := out.(type) {
			case *[]map[string]json.RawMessage:
				var m map[string]json.RawMessage
				_ = json.Unmarshal([]byte(l), &m)
				*o = append(*o, m)
			case *[]map[string]any:
				var m map[string]any
				_ = json.Unmarshal([]byte(l), &m)
				*o = append(*o, m)
			}
		}
	}
	lines("rpc.jsonl", &cc.rpc)
	lines("answers.jsonl", &cc.answers)
	lines("mcp.jsonl", &cc.mcp)
	return cc
}

func (c *fakeCodex) calls() int {
	es, _ := os.ReadDir(filepath.Join(c.dir, "calls"))
	return len(es)
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
