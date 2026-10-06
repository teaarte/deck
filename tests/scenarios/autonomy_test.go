package scenarios

import (
	"context"
	deckhome "deck2/internal/home"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// denied reports whether a deny rule of the permission rule syntax covers path or a folder above it.
func denied(rules []string, path string) bool {
	for _, r := range rules {
		_, pat, ok := strings.Cut(strings.TrimSuffix(r, ")"), "(/")
		if !ok {
			continue
		}
		dir, tree := strings.CutSuffix(pat, "/**")
		for p := path; p != filepath.Dir(p); p = filepath.Dir(p) {
			if ok, _ := filepath.Match(dir, p); ok && (tree || p == path) {
				return true
			}
		}
	}
	return false
}

type cliSettings struct {
	Permissions struct{ Deny []string }
	Sandbox     *struct {
		Enabled                  bool
		FailIfUnavailable        bool
		AllowUnsandboxedCommands *bool
		Network                  struct {
			AllowedDomains  []string
			StrictAllowlist bool
		}
	}
}

func TestCommandAgentRunsInAutoModeWithItsOwnWeb(t *testing.T) {
	c := newCLI(t)
	c.stream(0, "ok.jsonl")
	e, home := cliEnv(t, c)
	_ = os.WriteFile(filepath.Join(home, "deck.log"), []byte("deck listening url=http://127.0.0.1:7700/?token=x"), 0o600)
	e.createProject("other", plainManifest)
	p := e.createProject("notes", postManifest("http://127.0.0.1:1", ""))
	var id int64
	for id < 10 {
		id = e.run(p, "work")
		if got := e.waitState(id, "done", "error"); got.State != "done" {
			t.Fatalf("want done, got %+v", got)
		}
	}
	call := c.call(c.calls())
	for n := c.calls(); slices.Contains(call.args, "--resume"); n-- {
		call = c.call(n - 1)
	}
	data := filepath.Join(deckhome.Layout(home).Work(), "projects", fmt.Sprint(p), "data")
	if mode, _ := flag(call.args, "--permission-mode"); mode != "auto" {
		t.Fatalf("the agent runs in the auto mode of the CLI: %v", call.args)
	}
	if slices.Contains(call.args, "--disallowedTools") {
		t.Fatalf("the agent keeps the web of the CLI: %v", call.args)
	}
	if dir, _ := flag(call.args, "--add-dir"); dir != data || !strings.Contains(call.stdin, data) {
		t.Fatalf("the agent writes the data folder of its project and knows where it is: %v", call.args)
	}
	if st, err := os.Stat(data); err != nil || !st.IsDir() {
		t.Fatalf("the data folder exists before the agent starts: %v", err)
	}
	allowed, _ := flag(call.args, "--allowedTools")
	for _, tool := range []string{"Bash", "Write", "Edit", "NotebookEdit", "WebFetch", "WebSearch"} {
		if slices.Contains(strings.Split(allowed, ","), tool) {
			t.Fatalf("%s is left to the auto mode of the CLI, not allowed outright: %s", tool, allowed)
		}
	}
	var set cliSettings
	if err := json.Unmarshal([]byte(call.settings), &set); err != nil {
		t.Fatal(err)
	}
	if set.Sandbox != nil {
		t.Fatalf("a project with network has no sandbox of the CLI: %s", call.settings)
	}
	real, _ := filepath.EvalSymlinks(home)
	for _, h := range []string{home, real} {
		mine := []string{filepath.Join(deckhome.Layout(h).Work(), "runs", fmt.Sprint(id), "work", "a.txt"), filepath.Join(deckhome.Layout(h).Work(), "projects", fmt.Sprint(p), "data", "index")}
		for _, path := range mine {
			if denied(set.Permissions.Deny, path) {
				t.Fatalf("the agent reads and writes its own folders: %s", path)
			}
		}
		theirs := []string{filepath.Join(deckhome.Layout(h).State(), "token"), filepath.Join(deckhome.Layout(h).State(), "mcp", "mcp-1.json"), filepath.Join(deckhome.Layout(h).State(), "files", "3", "cv.pdf"),
			filepath.Join(deckhome.Layout(h).State(), "secrets", "svc"), filepath.Join(deckhome.Layout(h).State(), "deck.db-wal"), filepath.Join(deckhome.Layout(h).State(), "backups", "x"),
			filepath.Join(deckhome.Layout(h).State(), "mcp", "mcp-1.tmp"), filepath.Join(h, "deck.log")}
		if slices.ContainsFunc(set.Permissions.Deny, func(r string) bool { return strings.Contains(r, deckhome.Layout(h).Work()) }) {
			t.Fatalf("the rules keep only deck's state from the agent, not masks of other runs and projects its shell passes anyway: %v", set.Permissions.Deny)
		}
		for _, path := range theirs {
			for _, tool := range []string{"Read", "Edit"} {
				if !slices.ContainsFunc(set.Permissions.Deny, func(r string) bool { return strings.HasPrefix(r, tool+"(") && denied([]string{r}, path) }) {
					t.Fatalf("%s of %s is denied to the agent: %v", tool, path, set.Permissions.Deny)
				}
			}
		}
	}
}

const noNetworkManifest = `{"version": 9, "no_network": true,
  "commands": [{"name": "work", "title": "Work", "description": "Does work", "class": "internal",
    "executor": {"type": "agent", "prompt": "Do the work."}}]}`

func TestNoNetworkProjectAgentsHaveNoWebOfTheirOwn(t *testing.T) {
	c := newCLI(t)
	c.stream(0, "ok.jsonl")
	e, _ := cliEnv(t, c)
	p := e.createProject("health", noNetworkManifest)
	if got := e.waitState(e.run(p, "work"), "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	call := c.call(1)
	if off, _ := flag(call.args, "--disallowedTools"); off != "WebFetch,WebSearch" {
		t.Fatalf("an agent of a project without network has no web tools: %v", call.args)
	}
	if tools, _ := flag(call.args, "--tools"); !slices.Contains(strings.Split(tools, ","), "Bash") || strings.Contains(tools, "Web") {
		t.Fatalf("it gets only the listed offline tools, so a network tool of a later CLI is off too: %v", call.args)
	}
	var set cliSettings
	if err := json.Unmarshal([]byte(call.settings), &set); err != nil {
		t.Fatal(err)
	}
	if s := set.Sandbox; s == nil || !s.Enabled || !s.FailIfUnavailable || s.AllowUnsandboxedCommands == nil || *s.AllowUnsandboxedCommands ||
		!s.Network.StrictAllowlist || s.Network.AllowedDomains == nil || len(s.Network.AllowedDomains) != 0 {
		t.Fatalf("its shell runs in the sandbox of the CLI with no domain: %s", call.settings)
	}

	_, tok := e.token("terminal", "deck.*")
	open := strings.Replace(noNetworkManifest, `"no_network": true,`, "", 1)
	var w struct {
		Waiting int64
		Widens  []string
	}
	code, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "health", "manifest": open})
	if code != 200 || json.Unmarshal(out, &w) != nil || w.Waiting == 0 || !slices.Equal(w.Widens, []string{"no_network removed: agents get their own web"}) {
		t.Fatalf("removing no_network waits for the owner's button: %d %s", code, out)
	}
	if rev := e.manifestRevision("health"); rev != 1 {
		t.Fatalf("the project stays without network until the owner allows: revision %d", rev)
	}
	web := strings.Replace(noNetworkManifest, `"class": "internal",`, `"class": "internal", "requires": ["web"],`, 1)
	if code, out := e.call("deck.manifest_save", map[string]any{"project": "health", "manifest": web}); code != 422 || !strings.Contains(string(out), "requires web") {
		t.Fatalf("a command of a project without network cannot require web: %d %s", code, out)
	}
	e.must("deck.manifest_save", map[string]any{"project": "health", "manifest": open}, nil)
	if code, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "health", "manifest": noNetworkManifest}); code != 200 || strings.Contains(string(out), "waiting") {
		t.Fatalf("setting no_network narrows rights and applies at once: %d %s", code, out)
	}
}

func TestShellAndWebCommandsTakeOnlyAProviderInAutoModeWithItsWeb(t *testing.T) {
	c := newCLI(t)
	e, _ := cliEnv(t, c)
	for _, req := range []string{`["sandbox"]`, `["web"]`} {
		m := strings.Replace(plainManifest, `"class": "internal",`, `"class": "internal", "requires": `+req+`,`, 1)
		e.createProject("p"+fmt.Sprint(len(req)), m)
		var list []struct {
			Name      string
			Providers []string
		}
		e.must("deck.commands_list", map[string]any{"project": "p" + fmt.Sprint(len(req))}, &list)
		i := slices.IndexFunc(list, func(c struct {
			Name      string
			Providers []string
		}) bool {
			return strings.HasSuffix(c.Name, ".work")
		})
		if i < 0 || !slices.Contains(list[i].Providers, "claude-code") {
			t.Fatalf("requires %s: only a provider with a shell in auto mode and its own web fits: %+v", req, list)
		}
	}
}

// selfCheckWith runs the self-check on the fake CLI whose before script is given.
func selfCheckWith(t *testing.T, fixture, before, offline, status string) string {
	c := newCLI(t)
	c.stream(1, fixture)
	c.stream(2, "selfcheck_resume.jsonl")
	c.approving()
	if before != "" {
		b, _ := os.ReadFile(filepath.Join(c.dir, "before"))
		c.before(strings.Replace(string(b), `echo "$word" > word.txt`, before, 1))
	}
	if offline != "" {
		b, _ := os.ReadFile(filepath.Join(c.dir, "before"))
		c.before(strings.Replace(string(b), "selfcheck_offline.jsonl", offline, 1))
	}
	e := clockCLIEnv(t, c)
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	return e.waitHealth("provider:claude-code", status).Error
}

func TestSelfCheckIsRedWhenAutoModeStopsWorking(t *testing.T) {
	self, _ := os.Executable()
	for _, tc := range []struct{ name, fixture, before, want, offline string }{
		{"green", "selfcheck.jsonl", "", "", ""},
		{"asks about a write in its folder", "selfcheck.jsonl", `echo "$word" > word.txt; DECK_FAKE_MCP=approve '` + self + `' "$(sed -n 's/.*"url":"\([^"]*\)".*/\1/p' "$c/mcpconfig")" Bash '{"command":"echo"}'`, "auto mode: the CLI asked deck about", ""},
		{"write did not run", "selfcheck.jsonl", ":", "auto mode: the write in its own folder did not run", ""},
		{"no question", "selfcheck.jsonl", `echo "$word" > word.txt; return 0`, "permission prompt: the question did not reach", ""},
		{"hidden file read", "selfcheck_nodenial.jsonl", "", "secrets: the CLI did not refuse", ""},
		{"network in a project without it", "selfcheck.jsonl", "", "turn without network: curl in the sandbox of the CLI printed", "selfcheck_online.jsonl"},
		{"secret leaked", "selfcheck.jsonl", `echo "$word" > word.txt; word=$(cat "$(grep -o '/[^ ]*/secrets/key.txt' "$c/stdin")")`, "the agent read a file deck hides", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := "error"
			if tc.want == "" {
				status = "ok"
			}
			got := selfCheckWith(t, tc.fixture, tc.before, tc.offline, status)
			if tc.want == "" && got != "" || !strings.Contains(got, tc.want) {
				t.Fatalf("self-check error %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNoNetworkReachesAProcessStartedBeforeIt(t *testing.T) {
	c := newCLI(t)
	c.stream(0, "ok.jsonl")
	e, _ := cliEnv(t, c)
	p := e.createProject("notes", plainManifest)
	id := e.run(p, "work")
	e.waitState(id, "done")
	e.must("deck.manifest_save", map[string]any{"project": "notes", "manifest": strings.Replace(plainManifest, `"screens": []`, `"screens": [], "no_network": true`, 1)}, nil)
	e.must("deck.process_rework", map[string]any{"process": id, "remark": "once more"}, nil)
	e.waitState(id, "done", "error")
	waitCalls(t, c, 2)
	if call := c.call(2); !slices.Contains(call.args, "--resume") || !slices.Contains(call.args, "--tools") {
		t.Fatalf("a resumed session of a process started before no_network is without network too: %v", call.args)
	}
}
