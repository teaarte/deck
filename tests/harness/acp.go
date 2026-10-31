package harness

import (
	"context"
	"deck2/internal/app"
	"deck2/internal/health"
	"deck2/internal/sandbox"
	"deck2/internal/store/db"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeACP is a folder with the fake ACP agent, started as goose, and the streams it replays.
type fakeACP struct{ fakeBin }

func NewACP(t *testing.T) *fakeACP {
	t.Helper()
	dir := t.TempDir()
	self := Fakes
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
	return &fakeACP{fakeBin{T: t, Dir: dir, fixtures: Testdata("acp"), version: "agent", who: "the agent"}}
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

// SelfCheck makes calls 1 to 3 the self-check deck runs when the provider is added.
func (f *fakeACP) SelfCheck() {
	for i := 1; i <= 3; i++ {
		f.Stream(i, "selfcheck_"+strconv.Itoa(i)+".jsonl")
	}
}

type acpCall struct {
	Args      []string
	Env       map[string]string
	Cwd       string
	Rpc       []map[string]json.RawMessage
	Answers   []map[string]json.RawMessage
	Mcp       []map[string]any
	Model     []map[string]any
	Shell     []map[string]string
	port      string
	Violation string
}

func (f *fakeACP) Call(n int) acpCall {
	f.T.Helper()
	read := f.Files(n)
	c := acpCall{Env: map[string]string{}, Cwd: read("cwd"), Violation: read("violation"), Args: strings.Split(read("args"), "\n"), port: read("port")}
	for _, kv := range strings.Split(read("env"), "\n") {
		k, v, _ := strings.Cut(kv, "=")
		c.Env[k] = v
	}
	c.Rpc = JsonLines[map[string]json.RawMessage](read("rpc.jsonl"))
	c.Answers = JsonLines[map[string]json.RawMessage](read("answers.jsonl"))
	c.Mcp = JsonLines[map[string]any](read("mcp.jsonl"))
	c.Model = JsonLines[map[string]any](read("model.jsonl"))
	c.Shell = JsonLines[map[string]string](read("shell.jsonl"))
	return c
}

// Request is the params of the first request of a method deck sent, or nil.
func (c acpCall) Request(method string) json.RawMessage {
	for _, m := range c.Rpc {
		if string(m["method"]) == `"`+method+`"` {
			return m["params"]
		}
	}
	return nil
}

// FakeDirSandbox is the sandbox of this machine that also lets the fake agent, the test binary, run and write its records.
type FakeDirSandbox struct {
	sandbox.Sandbox
	Dir  string
	More []string
}

func (s FakeDirSandbox) Command(ctx context.Context, p sandbox.Policy) (*exec.Cmd, error) {
	self := Fakes
	p.Write, p.Read = append(append(p.Write, s.Dir), s.More...), append(p.Read, self)
	return s.Sandbox.Command(ctx, p)
}

// WithACP runs deck with the fake agent as goose; with llm it adds a model "acp" the agent runs on over llm, after
// its green self-check.
func WithACP(f *fakeACP, llm *FakeLLM) Option {
	return func(s *setup) {
		s.cfg = append(s.cfg, func(c *app.Config) {
			c.ACPBinaries = map[string]string{"goose": filepath.Join(f.Dir, "goose")}
			c.Sandbox = FakeDirSandbox{sandbox.New(), f.Dir, nil}
			c.ACPCallLimit = 2 * time.Second
		})
		if llm != nil {
			s.after = append(s.after, func(e *Env) { e.greenACP(f, llm) })
		}
	}
}

func (e *Env) greenACP(f *fakeACP, llm *FakeLLM) {
	e.T.Helper()
	f.SelfCheck()
	h := e.AddACP(f, llm)
	if h.Status != "ok" || !strings.Contains(h.Error, "tokens/s") || h.Speed <= 0 || h.Verdict != "" {
		e.T.Fatalf("the self-check of the fake agent is green and tells the speed of the model: %+v, %d calls", h, f.Calls())
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
func (e *Env) AddACP(f *fakeACP, llm *FakeLLM) db.Health {
	e.T.Helper()
	e.Secret("", "model_key", "sk-real-model-key")
	e.MustAgentModel(map[string]any{"name": "acp", "url": llm.URL, "model": "m", "key_secret": "model_key", "agent": "goose"})
	var h db.Health
	e.Until("the self-check of the ACP provider", func() bool {
		var err error
		h, err = health.Get(e.T.Context(), e.App.Store.Q, health.Of{}, "provider:acp")
		return err == nil && h.Status != "checking"
	})
	return h
}

// AcpProject creates a project whose command runs on the provider "acp".
func (e *Env) AcpProject(name, manifest string) int64 {
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
		run := filepath.Join(first.Dir, "goose")
		b, err := os.ReadFile(run)
		if err != nil {
			s.t.Fatal(err)
		}
		pick := strings.Replace(string(b), "\n", "\n[ \"$GOOSE_MODEL\" = local ] && exec '"+filepath.Join(second.Dir, "goose")+"' \"$@\"\n", 1)
		if !strings.Contains(string(b), second.Dir) {
			if err := os.WriteFile(run, []byte(pick), 0o755); err != nil {
				s.t.Fatal(err)
			}
		}
		s.cfg = append(s.cfg, func(c *app.Config) {
			c.ACPBinaries = map[string]string{"goose": run}
			c.Sandbox = FakeDirSandbox{sandbox.New(), first.Dir, []string{second.Dir}}
			c.ACPCallLimit = 2 * time.Second
		})
	}
}

// SetACP adds or changes a provider on an agent over llm with its key, its self-check replayed from call from on.
func (e *Env) SetACP(f *fakeACP, from int, name, agent string, llm *FakeLLM, noCloud bool) {
	e.T.Helper()
	for i := 1; i <= 3; i++ {
		f.Stream(from+i-1, "selfcheck_"+strconv.Itoa(i)+".jsonl")
	}
	e.Secret("", name+"-key", "sk-"+name)
	e.MustAgentModel(map[string]any{"name": name, "url": llm.URL, "model": name, "agent": agent,
		"key_secret": name + "-key", "no_cloud": noCloud})
	e.Until("the self-check of "+name, func() bool {
		h, err := health.Get(e.T.Context(), e.App.Store.Q, health.Of{}, "provider:"+name)
		return err == nil && h.Status == "ok"
	})
}

// AcpTurn is a turn of a fresh session that calls the model once and hands in result, or ends with text.
func AcpTurn(result string) string {
	end := `{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": ` + result + `}}}`
	if strings.HasPrefix(result, "{") {
		end = `{"mcp": {"tool": "result_submit", "arguments": {"result": ` + result + `}}}`
	}
	return AcpStream(AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, AcpModel, end,
		`{"end": "end_turn"}`)
}

const AcpInit = `{"reply": "initialize", "result": {"protocolVersion": 1, "agentCapabilities": {"mcpCapabilities": {"http": true}, "sessionCapabilities": {"resume": {}, "list": {}}}, "authMethods": []}}`

// AcpModel is a call of the model of a turn, which carries the tools of the agent.
const AcpModel = `{"model": {"model": "m", "messages": [{"role": "user", "content": "go"}], "tools": [{"type": "function", "function": {"name": "shell"}}]}}`

func AcpLLM(t *testing.T) *FakeLLM {
	return NewLLM(t, func(int, ChatRequest) ChatReply { return TextReply("ok") })
}

func AcpStream(lines ...string) string { return strings.Join(lines, "\n") }
