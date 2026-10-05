package scenarios

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/sandbox"
	"deck2/internal/store/db"
)

// fakeACP is a folder with the fake ACP agent, started as opencode or dsh, and the streams it replays.
type fakeACP struct{ fakeBin }

func newACP(t *testing.T) *fakeACP {
	t.Helper()
	dir := t.TempDir()
	self, _ := os.Executable()
	schema, err := os.ReadFile(acpSchema(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schema.json"), schema, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"opencode", "dsh"} {
		script := "#!/bin/sh\nexec env DECK_FAKE_ACP='" + dir + "' DECK_FAKE_ACP_SCHEMA='" + filepath.Join(dir, "schema.json") + "' '" + self + "' \"$@\"\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &fakeACP{fakeBin{t: t, dir: dir, fixtures: "testdata/acp", version: "agent", who: "the agent"}}
}

// acpSchema is the ACP schema of the SDK version deck is built with.
func acpSchema(t *testing.T) string {
	info, _ := debug.ReadBuildInfo()
	version := ""
	for _, d := range info.Deps {
		if d.Path == "github.com/coder/acp-go-sdk" {
			version = d.Version
		}
	}
	out, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil || version == "" {
		t.Fatalf("the ACP schema of the SDK: %v %q", err, version)
	}
	return filepath.Join(strings.TrimSpace(string(out)), "github.com", "coder", "acp-go-sdk@"+version, "schema", "schema.json")
}

// selfCheck makes calls 1 to 3 the self-check deck runs when the provider is added.
func (f *fakeACP) selfCheck() {
	for i := 1; i <= 3; i++ {
		f.stream(i, "selfcheck_"+strconv.Itoa(i)+".jsonl")
	}
}

type acpCall struct {
	args      []string
	env       map[string]string
	cwd       string
	rpc       []map[string]json.RawMessage
	answers   []map[string]json.RawMessage
	mcp       []map[string]any
	model     []map[string]any
	shell     []map[string]string
	port      string
	violation string
}

func (f *fakeACP) call(n int) acpCall {
	f.t.Helper()
	read := f.files(n)
	c := acpCall{env: map[string]string{}, cwd: read("cwd"), violation: read("violation"), args: strings.Split(read("args"), "\n"), port: read("port")}
	for _, kv := range strings.Split(read("env"), "\n") {
		k, v, _ := strings.Cut(kv, "=")
		c.env[k] = v
	}
	c.rpc = jsonLines[map[string]json.RawMessage](read("rpc.jsonl"))
	c.answers = jsonLines[map[string]json.RawMessage](read("answers.jsonl"))
	c.mcp = jsonLines[map[string]any](read("mcp.jsonl"))
	c.model = jsonLines[map[string]any](read("model.jsonl"))
	c.shell = jsonLines[map[string]string](read("shell.jsonl"))
	return c
}

// request is the params of the first request of a method deck sent, or nil.
func (c acpCall) request(method string) json.RawMessage {
	for _, m := range c.rpc {
		if string(m["method"]) == `"`+method+`"` {
			return m["params"]
		}
	}
	return nil
}

// fakeDirSandbox is the sandbox of this machine that also lets the fake agent, the test binary, run and write its records.
type fakeDirSandbox struct {
	sandbox.Sandbox
	dir  string
	more []string
}

func (s fakeDirSandbox) Command(ctx context.Context, p sandbox.Policy) (*exec.Cmd, error) {
	self, _ := os.Executable()
	p.Write, p.Read = append(append(p.Write, s.dir), s.more...), append(p.Read, self)
	return s.Sandbox.Command(ctx, p)
}

// acpEnv starts deck with the fake agent and adds a provider "acp" on it over llm, after its green self-check.
func acpEnv(t *testing.T, f *fakeACP, agent string, llm *fakeLLM, cfg ...func(*app.Config)) *Env {
	t.Helper()
	f.selfCheck()
	e, h := acpAdded(t, f, agent, llm, cfg...)
	if h.Status != "ok" || !strings.Contains(h.Error, "tokens/s") || h.Speed <= 0 || h.Verdict != "" {
		t.Fatalf("the self-check of the fake agent is green and tells the speed of the model: %+v, %d calls", h, f.calls())
	}
	var pl struct {
		Providers []struct {
			Name, Agent string
			Speed       float64
		}
	}
	e.must("deck.providers_list", nil, &pl)
	for _, p := range pl.Providers {
		if p.Name == "acp" && (p.Agent != agent || p.Speed != h.Speed) {
			t.Fatalf("the providers list shows the agent and the speed of the model: %+v", p)
		}
	}
	return e
}

// acpAdded adds the provider on the streams already set and gives the health its self-check ended with.
func acpAdded(t *testing.T, f *fakeACP, agent string, llm *fakeLLM, cfg ...func(*app.Config)) (*Env, db.Health) {
	t.Helper()
	e := newEnv(t, nil, append([]func(*app.Config){func(c *app.Config) {
		c.ACPBinaries = map[string]string{agent: filepath.Join(f.dir, agent)}
		c.Sandbox = fakeDirSandbox{sandbox.New(), f.dir, nil}
		c.ACPCallLimit = 2 * time.Second
	}}, cfg...)...)
	e.must("deck.secret_set", map[string]any{"name": "model_key", "value": "sk-real-model-key"}, nil)
	e.must("deck.provider_add", map[string]any{"name": "acp", "url": llm.URL, "model": "m", "key_secret": "model_key", "agent": agent}, nil)
	var h db.Health
	e.waitFor("the self-check of the ACP provider", func() bool {
		var err error
		h, err = e.App.Store.Q.GetHealth(t.Context(), "provider:acp")
		return err == nil && h.Status != "checking"
	})
	return e, h
}

// acpProject creates a project whose command runs on the provider "acp".
func (e *Env) acpProject(name, manifest string) int64 {
	e.T.Helper()
	p := e.createProject(name, manifest)
	var m struct {
		Commands []struct{ Name string } `json:"commands"`
	}
	_ = json.Unmarshal([]byte(manifest), &m)
	for _, c := range m.Commands {
		e.must("deck.provider_set", map[string]any{"command": name + "." + c.Name, "provider": "acp"}, nil)
	}
	return p
}

// acpPair starts deck on script with two fake ACP agents: opencode and dsh, each with its own calls.
func acpPair(t *testing.T, script Script) (e *Env, opencode, dsh *fakeACP) {
	t.Helper()
	opencode, dsh = newACP(t), newACP(t)
	e = newEnv(t, script, func(c *app.Config) {
		c.ACPBinaries = map[string]string{"opencode": filepath.Join(opencode.dir, "opencode"), "dsh": filepath.Join(dsh.dir, "dsh")}
		c.Sandbox = fakeDirSandbox{sandbox.New(), opencode.dir, []string{dsh.dir}}
		c.ACPCallLimit = 2 * time.Second
	})
	return e, opencode, dsh
}

// addACP adds or changes a provider on an agent over llm with its key, its self-check replayed from call from on.
func (e *Env) addACP(f *fakeACP, from int, name, agent string, llm *fakeLLM, noCloud bool) {
	e.T.Helper()
	for i := 1; i <= 3; i++ {
		f.stream(from+i-1, "selfcheck_"+strconv.Itoa(i)+".jsonl")
	}
	e.must("deck.secret_set", map[string]any{"name": name + "-key", "value": "sk-" + name}, nil)
	e.must("deck.provider_add", map[string]any{"name": name, "url": llm.URL, "model": "asked-model", "agent": agent,
		"key_secret": name + "-key", "no_cloud": noCloud}, nil)
	e.waitFor("the self-check of "+name, func() bool {
		h, err := e.App.Store.Q.GetHealth(e.T.Context(), "provider:"+name)
		return err == nil && h.Status == "ok"
	})
}

// acpTurn is a turn of a fresh session that calls the model once and hands in result, or ends with text.
func acpTurn(result string) string {
	end := `{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": ` + result + `}}}`
	if strings.HasPrefix(result, "{") {
		end = `{"mcp": {"tool": "result_submit", "arguments": {"result": ` + result + `}}}`
	}
	return acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, acpModel, end,
		`{"end": "end_turn"}`)
}
