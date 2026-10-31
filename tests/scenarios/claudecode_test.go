package scenarios

import (
	"context"
	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCLIGetsOnlyAllowlistedEnvironment(t *testing.T) {
	for k, v := range map[string]string{"ANTHROPIC_API_KEY": "sk-leak", "ANTHROPIC_BASE_URL": "http://leak", "CLAUDE_CODE_OAUTH_TOKEN": "leak",
		"CLAUDE_CODE_USE_BEDROCK": "leak", "DECK_HOME": "/leak", "AWS_SECRET_ACCESS_KEY": "leak", "LC_ALL": "C", "NODE_OPTIONS": "--no-warnings"} {
		t.Setenv(k, v)
	}
	c := harness.NewCLI(t)
	c.Stream(0, "ok.jsonl")
	e := harness.New(t, harness.WithCLI(c))
	home := e.Home
	p := e.Project("plain", harness.PlainManifest)
	id := e.Run(p, "work")
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	call := c.Call(1)
	for k, v := range call.Env {
		if strings.Contains(v, "leak") {
			t.Errorf("%s reached the CLI", k)
		}
	}
	for _, k := range []string{"PATH", "HOME", "LC_ALL", "NODE_OPTIONS", "MCP_TOOL_TIMEOUT"} {
		if _, ok := call.Env[k]; !ok {
			t.Errorf("%s must reach the CLI", k)
		}
	}
	if sid, ok := harness.Flag(call.Args, "--session-id"); !ok || sid != e.Session(id) {
		t.Fatalf("the CLI must run the session deck assigned: %q vs %q", sid, e.Session(id))
	}
	if _, ok := harness.Flag(call.Args, "--resume"); ok {
		t.Fatal("a new process must not resume")
	}
	if src, ok := harness.Flag(call.Args, "--setting-sources"); !ok || src != "" {
		t.Fatalf("a command agent must load no settings, got %q", src)
	}
	cfg, _ := harness.Flag(call.Args, "--mcp-config")
	cwd, _ := filepath.EvalSymlinks(call.Cwd)
	realCfg, _ := filepath.EvalSymlinks(filepath.Dir(cfg))
	if strings.HasPrefix(realCfg, cwd) || strings.HasPrefix(cfg, filepath.Join(deckhome.Layout(home).Work(), "runs")) {
		t.Fatalf("the MCP config %s must be outside the agent folder %s", cfg, cwd)
	}
	if call.McpMode != "-rw-------" || !strings.Contains(call.Mcp, "/mcp/") {
		t.Fatalf("the MCP config must be 600 and point at the gateway: %s %s", call.McpMode, call.Mcp)
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Fatalf("the MCP config must be removed after the turn: %v", err)
	}
	var set struct {
		Env          map[string]string
		APIKeyHelper *string `json:"apiKeyHelper"`
	}
	if err := json.Unmarshal([]byte(call.Settings), &set); err != nil {
		t.Fatal(err)
	}
	if call.SettingsMode != "-rw-------" || set.APIKeyHelper == nil || *set.APIKeyHelper != "" || set.Env["ANTHROPIC_API_KEY"] != "" ||
		set.Env["CLAUDE_CODE_USE_BEDROCK"] != "" {
		t.Fatalf("deck settings must blank out the key helper and API variables: %s %s", call.SettingsMode, call.Settings)
	}
	if f, _ := harness.Flag(call.Args, "--settings"); f == "" || !strings.HasPrefix(f, filepath.Join(deckhome.Layout(home).State(), "mcp")) {
		t.Fatalf("settings must be a file of deck: %q", f)
	}
}

func TestLargeValuesAreNotArguments(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "ok.jsonl")
	e := harness.New(t, harness.WithCLI(c))
	desc := strings.Repeat("d", 200*1024)
	m := strings.Replace(harness.IdeasManifest, `"properties": {"written": {"type": "integer"}}`,
		`"properties": {"written": {"type": "integer", "description": "`+desc+`"}}`, 1)
	p := e.Project("ideas", m)
	e.WaitState(e.Run(p, "collect"), "done", "error")
	call := c.Call(1)
	for _, a := range call.Args {
		if len(a) > 8*1024 {
			t.Fatalf("an argument of %d bytes: %.80s", len(a), a)
		}
	}
	if !strings.Contains(call.Stdin, `"subtype":"initialize"`) || !strings.Contains(call.Stdin, desc) || !strings.Contains(call.Stdin, "Write three ideas") {
		t.Fatalf("the schema and the prompt must come by stdin: %.300s", call.Stdin)
	}
}

func TestFakeCLIRejectsUnknownFlags(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	out, err := exec.Command(filepath.Join(c.Dir, "claude"), "-p", "--json-schema", "{}").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "unknown option '--json-schema'") {
		t.Fatalf("the fake must reject flags the adapter is not known to pass: %v %s", err, out)
	}
}

func TestCLIThatDoesNotExitAfterItsResultIsKilled(t *testing.T) {
	t.Parallel()
	for name, after := range map[string]string{
		"hangs":        `sleep 300 & echo $! > "$c/child"; wait`,
		"leaves child": `sleep 300 >/dev/null 2>&1 & echo $! > "$c/child"`,
	} {
		t.Run(name, func(t *testing.T) {
			c := harness.NewCLI(t)
			c.Stream(0, "ok.jsonl")
			c.After(after)
			e := harness.New(t, harness.WithCLI(c))
			p := e.Project("plain", harness.PlainManifest)
			start := time.Now()
			got := e.WaitState(e.Run(p, "work"), "done", "error")
			if got.State != "done" || !strings.Contains(string(got.Result), "ok") {
				t.Fatalf("the result must be kept, got %+v", got)
			}
			if d := time.Since(start); d > 4*time.Second {
				t.Fatalf("took %s", d)
			}
			b, _ := os.ReadFile(filepath.Join(c.Dir, "calls", "1", "child"))
			pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err != nil {
				t.Fatal(err)
			}
			e.Until(fmt.Sprintf("child %d of the CLI to exit", pid), func() bool { return syscall.Kill(pid, 0) != nil })
		})
	}
}

func TestTurnSucceedsOnlyStrictly(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", 512*1024)
	for _, tc := range []struct {
		name, fixture, after string
		skipStdin            bool
		prompt               string
		want                 string
	}{
		{name: "foreign session", fixture: "foreign_session.jsonl", want: "instead of"},
		{name: "result without session", fixture: "no_session.jsonl", want: "without an id"},
		{name: "error result", fixture: "error.jsonl", want: "API Error: 500"},
		{name: "no result", after: "exit 1", want: "without a result"},
		{name: "prompt not read", fixture: "ok.jsonl", skipStdin: true, prompt: big, want: "did not reach"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := harness.NewCLI(t)
			if tc.fixture != "" {
				c.Stream(0, tc.fixture)
			}
			if tc.after != "" {
				c.After(tc.after)
			}
			if tc.skipStdin {
				c.SkipStdin()
			}
			m := harness.PlainManifest
			if tc.prompt != "" {
				m = strings.Replace(m, "Do the work.", tc.prompt, 1)
			}
			e := harness.New(t, harness.WithCLI(c))
			p := e.Project("plain", m)
			got := e.WaitState(e.Run(p, "work"), "done", "error")
			if got.State != "error" || !strings.Contains(got.Error, tc.want) {
				t.Fatalf("want error with %q, got %+v", tc.want, got)
			}
		})
	}
}

func TestPromptMentionsOnlyConnectedTools(t *testing.T) {
	t.Parallel()
	for _, noQuestions := range []bool{false, true} {
		fake := &harness.Fake{NoQuestions: noQuestions, Script: func(context.Context, *harness.Call) provider.Result {
			return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
		}}
		e := harness.New(t, harness.WithProvider(fake))
		p := e.Project("ideas", harness.IdeasManifest)
		e.WaitState(e.Run(p, "collect"), "done")
		prompt := fake.Calls()[0].Prompt
		if strings.Contains(prompt, "AskUserQuestion") == noQuestions || !strings.Contains(prompt, "records_upsert") {
			t.Fatalf("question tool present=%v, prompt:\n%s", !noQuestions, prompt)
		}
	}
}

func TestSkillsReachTheCLIAsAPluginOutsideTheAgentFolder(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "ok.jsonl")
	e := harness.New(t, harness.WithCLI(c))
	home := e.Home
	skilled := strings.Replace(harness.PlainManifest, `"prompt": "Do the work."}`, `"prompt": "Do the work.", "skills": ["reader"]}`, 1)
	p := e.Project("plain", skilled)
	skill := "---\nname: reader\ndescription: Reads.\n---\nRead.\n"
	e.Must("deck.document_save", map[string]any{"project": "plain", "name": "skills/reader/SKILL.md", "kind": "skill", "text": skill}, nil)
	if got := e.WaitState(e.Run(p, "work"), "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	call := c.Call(1)
	dir, ok := harness.Flag(call.Args, "--plugin-dir")
	if !ok || strings.HasPrefix(dir, filepath.Join(deckhome.Layout(home).Work(), "runs")) {
		t.Fatalf("skills go to a plugin folder outside the agent's folders: %q", dir)
	}
	copied := filepath.Join(c.Dir, "calls", "1", "plugin")
	if b, _ := os.ReadFile(filepath.Join(copied, "skills", "reader", "SKILL.md")); string(b) != skill {
		t.Fatalf("the plugin holds the skill: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(copied, ".claude-plugin", "plugin.json")); !strings.Contains(string(b), `"deck"`) {
		t.Fatalf("the plugin has its manifest: %q", b)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the plugin folder is removed after the turn: %v", err)
	}

	e.Project("bare", harness.PlainManifest)
	e.WaitState(e.Start("bare", "work", nil), "done")
	if _, ok := harness.Flag(c.Call(2).Args, "--plugin-dir"); ok {
		t.Fatal("a command without skills gets no plugin")
	}
}

func TestModelCallsCountTokensFromTheTurnResult(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "ok.jsonl")
	e := harness.New(t, harness.WithCLI(c))
	p := e.Project("plain", harness.PlainManifest)
	id := e.Run(p, "work")
	e.WaitState(id, "done")
	var calls []map[string]any
	e.ModelCalls("", id, &calls)
	if len(calls) != 1 {
		t.Fatalf("one turn of one model is one call: %v", calls)
	}
	want := map[string]any{"gen_ai.provider.name": "claude-code", "gen_ai.response.model": "claude-opus-5-5[1m]",
		"gen_ai.usage.input_tokens": 2.0, "gen_ai.usage.output_tokens": 4.0, "gen_ai.usage.cache_read.input_tokens": 10704.0,
		"gen_ai.usage.cache_creation.input_tokens": 4987.0, "duration_ms": 2244.0, "cost_usd": 0.042124800000000004}
	for k, v := range want {
		if calls[0][k] != v {
			t.Fatalf("%s is %v, want %v from the result of the turn: %v", k, calls[0][k], v, calls[0])
		}
	}
}

func TestClaudeCodeLimitPausesUntilItsReset(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(1, "ratelimit_rejected.jsonl")
	c.Stream(0, "ok.jsonl")
	e := harness.ClockCLIEnv(t, c)
	e.Clock.Add(time.Unix(1790379000, 0).Add(-2 * time.Hour).Sub(e.Clock.Now()))
	p := e.Project("plain", harness.PlainManifest)
	id := e.Run(p, "work")
	e.WaitState(id, "rate_limited")
	pp, _ := e.App.Store.Q.GetProcess(context.Background(), id)
	if pp.PauseUntilMs.Int64 != 1790379000*1000 {
		t.Fatalf("the next try is the reset time of the refusal: %d", pp.PauseUntilMs.Int64)
	}
	e.Clock.Add(time.Unix(1790379000, 1).Sub(e.Clock.Now()))
	e.Sweep()
	if got := e.WaitState(id, "done", "error"); got.State != "done" || !slices.Contains(c.Call(2).Args, "--resume") {
		t.Fatalf("after the reset the session is resumed: %+v %v", got, c.Call(2).Args)
	}
}

// TestClaudeCodeShellRunsInItsSandboxOnlyWithoutNetwork: the sandbox of the CLI closes the shell of a project
// without network; elsewhere the shell renders, binds its ports, uses the GPU and writes its caches.
func TestClaudeCodeShellRunsInItsSandboxOnlyWithoutNetwork(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "ok.jsonl")
	e := harness.New(t, harness.WithCLI(c))
	for i, tc := range []struct {
		domains string
		sandbox bool
	}{
		{`"domains": {"read": ["data.example.org"]},`, false},
		{`"domains": {"read": ["*"]},`, false},
		{`"no_network": true,`, true},
	} {
		p := e.Project(fmt.Sprintf("ideas%d", i), strings.Replace(harness.IdeasManifest, `"version": 13,`, `"version": 13, `+tc.domains, 1))
		e.WaitState(e.Run(p, "collect"), "done", "error")
		var set struct {
			Sandbox *struct {
				Enabled                  bool `json:"enabled"`
				AllowUnsandboxedCommands bool `json:"allowUnsandboxedCommands"`
				Network                  struct {
					AllowedDomains []string `json:"allowedDomains"`
				} `json:"network"`
			} `json:"sandbox"`
		}
		call := c.Call(c.Calls())
		if err := json.Unmarshal([]byte(call.Settings), &set); err != nil {
			t.Fatal(err)
		}
		closed := set.Sandbox != nil && set.Sandbox.Enabled && !set.Sandbox.AllowUnsandboxedCommands && len(set.Sandbox.Network.AllowedDomains) == 0
		if closed != tc.sandbox || !tc.sandbox && set.Sandbox != nil {
			t.Fatalf("%s: the shell runs in the sandbox of the CLI: %v, want %v: %s", tc.domains, closed, tc.sandbox, call.Settings)
		}
	}
}

// TestClaudeCodeReadsInItsFoldersAndShowsWhatItRefused: auto mode is told the folders of the agent, and reading in
// them only, keeping its own rules; a call the CLI refused is a line of the process log with why.
func TestClaudeCodeReadsInItsFoldersAndShowsWhatItRefused(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "denied.jsonl")
	e := harness.New(t, harness.WithCLI(c))
	id := e.Run(e.Project("ideas", harness.IdeasManifest), "collect")
	e.WaitState(id, "done", "error")
	call := c.Call(c.Calls())
	var set struct {
		AutoMode struct{ Environment, Allow []string } `json:"autoMode"`
	}
	if err := json.Unmarshal([]byte(call.Settings), &set); err != nil {
		t.Fatal(err)
	}
	folders := []string{call.Cwd}
	for i, a := range call.Args {
		if a == "--add-dir" {
			folders = append(folders, call.Args[i+1])
		}
	}
	env, allow := strings.Join(set.AutoMode.Environment, "\n"), strings.Join(set.AutoMode.Allow, "\n")
	if len(folders) < 2 || !slices.Contains(set.AutoMode.Environment, "$defaults") || !slices.Contains(set.AutoMode.Allow, "$defaults") ||
		!strings.Contains(allow, "inside the agent folders") {
		t.Fatalf("auto mode keeps its rules and allows reads in the folders of the agent: %d folders, %s", len(folders), call.Settings)
	}
	for _, f := range folders {
		if !strings.Contains(env, f) {
			t.Fatalf("auto mode is told the folder %s: %s", f, env)
		}
	}
	for _, l := range e.Log(id) {
		if l["kind"] == "denied" && l["title"] == "Bash" && strings.Contains(fmt.Sprint(l["text"]), "has been denied") {
			return
		}
	}
	t.Fatalf("the refused call is a line of the log: %+v", e.Log(id))
}
