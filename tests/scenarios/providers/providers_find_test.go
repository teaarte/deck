package providers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/provider/codex"
	"deck2/internal/sandbox"
	"deck2/tests/harness"
)

// TestDeckFindsACLIInTheFolderOfItsInstallerOnAShortPath: started by a launcher or a service with a PATH of the system
// alone, deck finds Claude Code in ~/.local/bin, checks it and gives the CLI that folder on its PATH.
func TestDeckFindsACLIInTheFolderOfItsInstallerOnAShortPath(t *testing.T) {
	owner := harness.OwnerHome(t)
	c := harness.NewCLI(t)
	c.Stream(1, "selfcheck.jsonl")
	c.Stream(2, "selfcheck_resume.jsonl")
	c.Approving()
	version(c, provider.CIVersion("CLAUDE_VERSION"))
	bin := filepath.Join(owner, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := harness.WriteProgram(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexec "+filepath.Join(c.Dir, "claude")+" \"$@\"\n")); err != nil {
		t.Fatal(err)
	}
	shell := filepath.Join(t.TempDir(), "sh")
	if err := harness.WriteProgram(shell, []byte("#!/bin/sh\nexit 0\n")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", owner)
	t.Setenv("SHELL", shell)
	t.Setenv("PATH", "/usr/bin:/bin")
	if got, err := sandbox.Find("claude"); err != nil || got != filepath.Join(bin, "claude") {
		t.Fatalf("deck finds claude in ~/.local/bin before any other: %q %v", got, err)
	}
	dir := t.TempDir()
	a := c.Adapter(dir)
	a.Binary, a.CI = "claude", provider.CIVersion("CLAUDE_VERSION")
	e := harness.New(t, harness.WithHome(dir), harness.WithProvider(a))
	e.App.Providers.Checks(context.Background(), time.Now())
	if h := e.WaitHealth("provider:claude-code", "ok"); h.Version != provider.CIVersion("CLAUDE_VERSION") {
		t.Fatalf("the CLI deck found passes its self-check on the version CI tests: %+v", h)
	}
	if path := c.Call(1).Env["PATH"]; !strings.Contains(path, bin) {
		t.Fatalf("the CLI runs with the folder deck found it in on its PATH: %q", path)
	}
}

// TestACLIThatIsNotInstalledSaysHowToInstallIt: the card of a built-in CLI deck does not find gives the command that
// installs it, in the owner's language.
func TestACLIThatIsNotInstalledSaysHowToInstallIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	missing := &codex.Adapter{Binary: filepath.Join(dir, "codex"), Home: home.Layout(dir), Dir: home.Layout(dir).Codex(), Linger: time.Second}
	main := &harness.Fake{Script: func(context.Context, *harness.Call) provider.Result { return provider.Result{Text: "ok"} }}
	e := harness.New(t, harness.WithHome(dir), harness.WithProvider(main, missing))
	for lang, want := range map[string]string{"": "not installed; install it: ", "ru": "не установлено; установите: "} {
		var v struct {
			Agents []struct{ Name, Status, Error string }
		}
		e.Must("deck.models_list", map[string]any{"lang": lang}, &v)
		var got string
		for _, a := range v.Agents {
			if a.Name == "codex" && a.Status == "missing" {
				got = a.Error
			}
		}
		if !strings.HasPrefix(got, want) || !strings.HasSuffix(got, "npm install -g @openai/codex") {
			t.Fatalf("a missing CLI says how to install it (%q): %q, %+v", lang, got, v.Agents)
		}
	}
}

// TestACLIOfAnotherVersionWarnsAndWorks: a CLI of another version than CI tests deck with passes its self-check with a
// warning that names both versions, and takes work.
func TestACLIOfAnotherVersionWarnsAndWorks(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(1, "selfcheck.jsonl")
	c.Stream(2, "selfcheck_resume.jsonl")
	c.Stream(0, "ok.jsonl")
	c.Approving()
	version(c, "2.1.300")
	dir := t.TempDir()
	a := c.Adapter(dir)
	a.CI = "2.1.281"
	e := harness.New(t, harness.WithHome(dir), harness.WithProvider(a))
	e.App.Providers.Checks(context.Background(), time.Now())
	h := e.WaitHealth("provider:claude-code", "warning")
	if h.Version != "2.1.300" || !strings.Contains(h.Error, "2.1.281") || !strings.Contains(h.Error, "2.1.300") {
		t.Fatalf("another version warns with both: %+v", h)
	}
	if got := e.WaitState(e.Run(e.Project("plain", harness.PlainManifest), "work"), "done", "error"); got.State != "done" {
		t.Fatalf("a CLI of another version takes work: %+v", got)
	}
}
