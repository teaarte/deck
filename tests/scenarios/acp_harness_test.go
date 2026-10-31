package scenarios

import (
	"context"
	"deck2/internal/health"
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

// fakeACP is a folder with the fake ACP agent, started as goose, and the streams it replays.
type fakeACP struct{ fakeBin }

func newACP(t *testing.T) *fakeACP {
	t.Helper()
	dir := t.TempDir()
	self := fakes
	schema, err := os.ReadFile(acpSchema(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schema.json"), schema, 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexec env DECK_FAKE_ACP='" + dir + "' DECK_FAKE_ACP_SCHEMA='" + filepath.Join(dir, "schema.json") + "' '" + self + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "goose"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &fakeACP{fakeBin{t: t, dir: dir, fixtures: Testdata("acp"), version: "agent", who: "the agent"}}
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
	self := fakes
	p.Write, p.Read = append(append(p.Write, s.dir), s.more...), append(p.Read, self)
	return s.Sandbox.Command(ctx, p)
}

// WithACP runs deck with the fake agent as goose; with llm it adds a model "acp" the agent runs on over llm, after
// its green self-check.
func WithACP(f *fakeACP, llm *fakeLLM) Option {
	return func(s *setup) {
		s.cfg = append(s.cfg, func(c *app.Config) {
			c.ACPBinaries = map[string]string{"goose": filepath.Join(f.dir, "goose")}
			c.Sandbox = fakeDirSandbox{sandbox.New(), f.dir, nil}
			c.ACPCallLimit = 2 * time.Second
		})
		if llm != nil {
			s.after = append(s.after, func(e *Env) { e.greenACP(f, llm) })
		}
	}
}

func (e *Env) greenACP(f *fakeACP, llm *fakeLLM) {
	e.T.Helper()
	f.selfCheck()
	h := e.AddACP(f, llm)
	if h.Status != "ok" || !strings.Contains(h.Error, "tokens/s") || h.Speed <= 0 || h.Verdict != "" {
		e.T.Fatalf("the self-check of the fake agent is green and tells the speed of the model: %+v, %d calls", h, f.calls())
	}
	var pl struct {
		Models []struct {
			Name, Agent string
			Speed       float64
		}
	}
	e.Must("deck.models_list", nil, &pl)
	for _, p := range pl.Models {
		if p.Name == "acp" && (p.Agent != "goose" || p.Speed != h.Speed) {
			e.T.Fatalf("the models list shows the agent and the speed of the model: %+v", p)
		}
	}
}

// AddACP adds the model "acp" goose runs over llm on the streams already set and gives the health its self-check
// ended with.
func (e *Env) AddACP(f *fakeACP, llm *fakeLLM) db.Health {
	e.T.Helper()
	e.Must("deck.secret_set", map[string]any{"name": "model_key", "value": "sk-real-model-key"}, nil)
	e.mustAgentModel(map[string]any{"name": "acp", "url": llm.URL, "model": "m", "key_secret": "model_key", "agent": "goose"})
	var h db.Health
	e.Until("the self-check of the ACP provider", func() bool {
		var err error
		h, err = health.Get(e.T.Context(), e.App.Store.Q, health.Of{}, "provider:acp")
		return err == nil && h.Status != "checking"
	})
	return h
}

// acpProject creates a project whose command runs on the provider "acp".
func (e *Env) acpProject(name, manifest string) int64 {
	e.T.Helper()
	p := e.Project(name, manifest)
	var m struct {
		Commands []struct{ Name string } `json:"commands"`
	}
	_ = json.Unmarshal([]byte(manifest), &m)
	for _, c := range m.Commands {
		e.UseModels(name+"."+c.Name, "acp")
	}
	return p
}

// WithACPPair runs deck with two fake Goose agents, each with its own calls: a model named local runs on the second.
func WithACPPair(first, second *fakeACP) Option {
	return func(s *setup) {
		run := filepath.Join(first.dir, "goose")
		b, err := os.ReadFile(run)
		if err != nil {
			s.t.Fatal(err)
		}
		pick := strings.Replace(string(b), "\n", "\n[ \"$GOOSE_MODEL\" = local ] && exec '"+filepath.Join(second.dir, "goose")+"' \"$@\"\n", 1)
		if !strings.Contains(string(b), second.dir) {
			if err := os.WriteFile(run, []byte(pick), 0o755); err != nil {
				s.t.Fatal(err)
			}
		}
		s.cfg = append(s.cfg, func(c *app.Config) {
			c.ACPBinaries = map[string]string{"goose": run}
			c.Sandbox = fakeDirSandbox{sandbox.New(), first.dir, []string{second.dir}}
			c.ACPCallLimit = 2 * time.Second
		})
	}
}

// addACP adds or changes a provider on an agent over llm with its key, its self-check replayed from call from on.
func (e *Env) addACP(f *fakeACP, from int, name, agent string, llm *fakeLLM, noCloud bool) {
	e.T.Helper()
	for i := 1; i <= 3; i++ {
		f.stream(from+i-1, "selfcheck_"+strconv.Itoa(i)+".jsonl")
	}
	e.Must("deck.secret_set", map[string]any{"name": name + "-key", "value": "sk-" + name}, nil)
	e.mustAgentModel(map[string]any{"name": name, "url": llm.URL, "model": name, "agent": agent,
		"key_secret": name + "-key", "no_cloud": noCloud})
	e.Until("the self-check of "+name, func() bool {
		h, err := health.Get(e.T.Context(), e.App.Store.Q, health.Of{}, "provider:"+name)
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
