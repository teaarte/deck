package box

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"deck2/internal/app"
	deckhome "deck2/internal/home"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
	"deck2/tests/harness"
)

const listScript = harness.PythonLib + `
def there(p):
    return os.path.lexists(p)
print(json.dumps({"top": sorted(os.listdir("/")), "var": sorted(os.listdir("/var")) if there("/var") else [],
  "dri": there("/dev/dri"), "shadow": there("/etc/shadow"), "devices": sorted(os.listdir("/dev"))}))
`

// TestABoxSeesOnlyWhatItLists: on Linux a script sees nothing its box does not list, such as the disks of the host or
// the graphics card without the right; with the right it has the card the host has.
func TestABoxSeesOnlyWhatItLists(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the box of macOS stays sandbox-exec until deck moves into a Linux container there")
	}
	// A folder of another disk on PATH, as the drives of Windows are in WSL, stays out of the box too.
	t.Setenv("PATH", "/mnt"+string(os.PathListSeparator)+os.Getenv("PATH"))
	e := harness.New(t)
	e.Project("plain", harness.ScriptManifest("collect", "", ""))
	e.SaveScript("plain", "scripts/main.py", listScript)
	e.Project("render", harness.ScriptManifest("collect", "", `, "gpu": true`))
	e.SaveScript("render", "scripts/main.py", listScript)
	var got struct {
		Top, Var, Devices []string
		Dri, Shadow       bool
	}
	p := e.WaitState(e.Start("plain", "collect", nil), "done", "error")
	if err := json.Unmarshal(p.Result, &got); err != nil || p.State != "done" {
		t.Fatalf("the script runs: %+v", p)
	}
	for _, hidden := range []string{"Users", "Volumes", "mnt", "media", "srv", "root", "boot", "sys"} {
		if _, err := os.Stat("/" + hidden); err == nil && strings.Contains(" "+strings.Join(got.Top, " ")+" ", " "+hidden+" ") {
			t.Errorf("/%s of the host is not in the box: %v", hidden, got.Top)
		}
	}
	for _, v := range got.Var {
		if v != "tmp" {
			t.Errorf("of /var the box has only its own temp folder: %v", got.Var)
		}
	}
	if got.Dri || got.Shadow {
		t.Fatalf("without the right no graphics card, and no file of /etc the box does not list: %+v", got)
	}
	p = e.WaitState(e.Start("render", "collect", nil), "done", "error")
	_ = json.Unmarshal(p.Result, &got)
	if _, err := os.Stat("/dev/dri"); p.State != "done" || got.Dri != (err == nil) {
		t.Fatalf("with the right the box has the graphics card of the host: %+v %s", got, p.Result)
	}
	_, tok := e.Token("terminal", "deck.*")
	e.As(tok).Call("deck.project_create", map[string]any{"name": "asks", "manifest": harness.ScriptManifest("collect", "", `, "gpu": true`)})
	widens := e.ProjectRow("asks").Rights
	if len(widens) != 1 || widens[0] != "gpu: its programs use the graphics card" {
		t.Fatalf("the graphics card is a right the owner gives: %v", widens)
	}
}

const neighbourScript = harness.PythonLib + `
inp = json.load(sys.stdin)
seen = []
for pid in os.listdir("/proc"):
    if not pid.isdigit():
        continue
    for part in ("cmdline", "environ"):
        try:
            with open("/proc/%s/%s" % (pid, part), "rb") as f:
                if inp["secret"].encode() in f.read():
                    seen.append(pid + "/" + part)
        except OSError:
            pass
print(json.dumps({"seen": seen}))
`

// TestASecretOfAServerIsNotInTheCommandLineNorSeenByAnotherBox: the secret an MCP server gets in its environment
// goes to the box past the command line of bwrap, and the script of another module sees neither.
func TestASecretOfAServerIsNotInTheCommandLineNorSeenByAnotherBox(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("on macOS the arguments of a neighbour are readable until deck moves into a Linux container there")
	}
	t.Parallel()
	const secret = "sk-server-secret-4711"
	e := harness.New(t)
	e.Project("holder", harness.Manifest(fmt.Sprintf(`{"tools": {"mcp": {"servers": {"fake": {"command": %q,
    "env": {"DECK_FAKE_MCP": "project", "FAKE_TOKEN": "$secret.tok"}, "toolFilter": ["env"], "classes": {"env": "internal"}}}}}}`, harness.Fakes)))
	e.Secret("holder", "tok", secret)
	e.App.Signins.Servers(context.Background(), e.ProjectID("holder"))
	var said string
	e.Until("the server to run with its secret", func() bool {
		out, _ := e.App.Gateway.CallAs(context.Background(), registry.Actor{Kind: registry.Agent, Workspace: 1, ProjectID: e.ProjectID("holder")},
			"holder.fake.env", json.RawMessage(`{}`))
		b, _ := json.Marshal(out)
		said = string(b)
		return strings.Contains(said, fmt.Sprintf("len=%d", len(secret)))
	})
	cmdlines, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	for _, c := range cmdlines {
		if b, _ := os.ReadFile(c); strings.Contains(string(b), secret) {
			t.Fatalf("no command line of the machine holds the secret: %s %q", c, b)
		}
	}
	e.Project("prying", harness.ScriptManifest("collect", "", ""))
	e.SaveScript("prying", "scripts/main.py", neighbourScript)
	p := e.WaitState(e.Start("prying", "collect", map[string]any{"secret": secret}), "done", "error")
	if p.State != "done" || !strings.Contains(string(p.Result), `"seen":[]`) && !strings.Contains(string(p.Result), `"seen": []`) {
		t.Fatalf("the script of another module sees the secret in no process: %+v %s", p, p.Result)
	}
}

// TestTheBoxesACrashedDeckLeftAreKilledByTheirMarks: the script, MCP server and agent a deck left running when it
// crashed are killed by the marks of their boxes when deck starts again; a box of another deck stays.
func TestTheBoxesACrashedDeckLeftAreKilledByTheirMarks(t *testing.T) {
	t.Parallel()
	home, other := t.TempDir(), t.TempDir()
	left := map[string]*sandbox.Proc{}
	for _, b := range []struct {
		home, kind string
	}{{home, sandbox.KindScript}, {home, sandbox.KindMCP}, {home, sandbox.KindAgent}, {other, sandbox.KindScript}} {
		sb := sandbox.New(deckhome.Layout(b.home))
		box := filepath.Join(b.home, "box-"+b.kind)
		p, err := sb.Run(context.Background(), sandbox.Box{Kind: b.kind, Mark: sb.Mark(b.kind, "7"), Argv: []string{"sleep", "300"},
			Dir: box, Home: box}, sandbox.IO{Stdin: strings.NewReader("")})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Kill)
		left[b.home+" "+b.kind] = p
	}
	harness.New(t, harness.WithHome(home))
	for name, p := range left {
		if strings.HasPrefix(name, other) {
			continue
		}
		harness.Receive(t, "the end of the "+name+" box the crashed deck left", p.Done())
	}
	select {
	case <-left[other+" "+sandbox.KindScript].Done():
		t.Fatal("the box of another deck stays")
	default:
	}
}

// TestWithoutABoxDeckStartsNoForeignProgramAndSaysWhatToFix: a machine where bwrap is missing or refused runs no
// script and says in health what the administrator adds.
func TestWithoutABoxDeckStartsNoForeignProgramAndSaysWhatToFix(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the box of macOS stays sandbox-exec until deck moves into a Linux container there")
	}
	t.Parallel()
	refusing := filepath.Join(t.TempDir(), "bwrap")
	if err := os.WriteFile(refusing, []byte("#!/bin/sh\necho 'bwrap: Can'\\''t mount proc on /newroot/proc: Operation not permitted' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for program, says := range map[string]string{filepath.Join(t.TempDir(), "none"): "bubblewrap is not installed",
		refusing: "systempaths=unconfined is missing"} {
		e := harness.New(t, harness.WithConfig(func(c *app.Config) { c.Sandbox.Program = program }))
		h := e.WaitHealth("sandbox", "error")
		if !strings.Contains(h.Error, says) || program == refusing && (!strings.Contains(h.Error, "apparmor=deck-box") ||
			!strings.Contains(h.Error, "apparmor_parser -r /etc/apparmor.d/deck-box") || !strings.Contains(h.Error, "seccomp=./seccomp.json")) {
			t.Fatalf("health says what to fix: %+v", h)
		}
		e.Project("tools", harness.ScriptManifest("collect", "", ""))
		e.SaveScript("tools", "scripts/main.py", "print('{}')\n")
		if p := e.WaitState(e.Start("tools", "collect", nil), "done", "error"); p.State != "error" || !strings.Contains(p.Error, "without its box") {
			t.Fatalf("no script runs without its box: %+v", p)
		}
	}
}
