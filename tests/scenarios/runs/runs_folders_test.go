package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestAgentCommandWorksInTheProjectFolder: an agent command with folder runs in the project folder the owner
// added, as a repo when it is one, and a rework goes back to the same folder; the Projects tab lists folders.
func TestAgentCommandWorksInTheProjectFolder(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	p := e.Project("code", harness.TestManifest([]string{harness.FolderCommand("run", "true"), harness.AgentCommand("plain", ""),
		harness.FolderCommand("docs", `"notes"`)}, nil))
	e.Refused("code.run", map[string]any{}, 422, "has no folder yet")

	repo := harness.RepoFolder(t)
	var added struct{ Name, Path string }
	e.Must("deck.folder_add", map[string]any{"project": "code", "path": repo}, &added)
	if added.Name != "my-repo" || added.Path != repo {
		t.Fatalf("a folder gets the name of its own folder and its real path: %+v", added)
	}
	e.Refused("deck.folder_add", map[string]any{"project": "code", "path": repo, "name": "again"}, 422, "already has folder")

	id := e.Run(p, "run")
	e.WaitState(id, "done")
	c := e.Fake.Calls()[0]
	if c.Cwd != repo || !c.Repo || !strings.Contains(c.Prompt, "You work in the project folder "+repo) {
		t.Fatalf("the agent works in the project folder as in a repo: cwd %s repo %v", c.Cwd, c.Repo)
	}
	plain := e.Run(p, "plain")
	e.WaitState(plain, "done")
	if c := e.Fake.Calls()[1]; c.Cwd == repo || c.Repo || !strings.Contains(c.Cwd, filepath.Join("runs", "")) {
		t.Fatalf("a command without folder keeps a work folder of its own: %s", c.Cwd)
	}
	e.Must("deck.process_rework", map[string]any{"process": id, "remark": "once more"}, nil)
	e.Until("the rework to run", func() bool { return len(e.Fake.Calls()) == 3 })
	e.WaitState(id, "done")
	if c := e.Fake.Calls()[2]; c.Cwd != repo || !c.Resume {
		t.Fatalf("a rework resumes the session in the same folder: %s resume %v", c.Cwd, c.Resume)
	}

	e.Refused("code.docs", map[string]any{}, 422, "has no folder notes")
	notes := t.TempDir()
	e.Must("deck.folder_add", map[string]any{"project": "code", "path": notes, "name": "notes"}, nil)
	e.Refused("code.run", map[string]any{}, 422, "has 2 folders")
	docs := e.Run(p, "docs")
	e.WaitState(docs, "done")
	if c := e.Fake.Calls()[3]; c.Repo || !strings.HasSuffix(c.Cwd, filepath.Base(notes)) {
		t.Fatalf("a named folder that is no repo: %s repo %v", c.Cwd, c.Repo)
	}

	var list []struct {
		Name    string
		Folders []struct{ Name, Path string }
	}
	e.ProjectsOf(&list)
	if len(list) != 1 || len(list[0].Folders) != 2 || list[0].Folders[0].Name != "my-repo" {
		t.Fatalf("projects_list gives the folders: %+v", list)
	}
	var items []harness.ListItem
	_ = json.Unmarshal(e.SettingsBlock("Modules", nil)["items"], &items)
	if len(items) != 2 || items[0].Meta != "" || strings.Contains(fmt.Sprint(items[1].Actions), "project_delete") {
		t.Fatalf("the card of a module that runs says only its name: %+v", items)
	}
	if own := harness.BlockData[[]harness.ListItem](t, e.ScreenBlock("code", "settings", "Module", nil)["items"]); len(own) != 1 || !strings.Contains(own[0].Meta, "folders: my-repo, notes") {
		t.Fatalf("the settings of the module show its folders: %+v", own)
	}
	if code, out := e.Press(e.RowAction("code", "settings", "deck.folder_remove", map[string]any{"name": "notes"})); code != 200 {
		t.Fatalf("the remove button of a folder on the settings of the project works: %d %s", code, out)
	}
	e.Refused("deck.folder_remove", map[string]any{"project": "code", "name": "notes"}, 404, "no folder notes")
	if _, err := os.Stat(notes); err != nil {
		t.Fatalf("removing a folder leaves it on disk: %v", err)
	}
	again := e.Run(p, "run")
	e.WaitState(again, "done")

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	e.Refused("code.run", map[string]any{}, 422, "is gone")
}

// TestSecondAgentInTheSameFolderWaitsForTheLock: two projects share a folder, one by a link; the second agent
// waits for it without reaching its provider, says so in the cockpit and runs when the first finishes.
func TestSecondAgentInTheSameFolderWaitsForTheLock(t *testing.T) {
	t.Parallel()
	release := make(chan struct{}, 10)
	e := harness.New(t, harness.WithAgent(held(release)))
	repo := harness.RepoFolder(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	a := e.Project("front", harness.TestManifest([]string{harness.FolderCommand("run", "true")}, nil))
	b := e.Project("back", harness.TestManifest([]string{harness.FolderCommand("run", "true")}, nil))
	e.Must("deck.folder_add", map[string]any{"project": "front", "path": repo}, nil)
	e.Must("deck.folder_add", map[string]any{"project": "back", "path": link, "name": "repo"}, nil)

	first := e.Run(a, "run")
	e.Until("the first agent at its provider", func() bool { return len(e.Fake.Calls()) == 1 })
	second, third := e.Run(b, "run"), e.Run(a, "run")
	e.Until("the others to wait for the folder", func() bool {
		return states(e, []int64{first, second, third}) == "running waiting_folder waiting_folder"
	})
	waits := 0
	for _, it := range e.CockpitRunning() {
		if it.Text == "waits for folder "+repo {
			waits++
		}
	}
	if waits != 2 {
		t.Fatalf("running now says which wait for the folder: %+v", e.CockpitRunning())
	}
	e.Must("deck.process_stop", map[string]any{"process": third}, nil)
	e.WaitState(third, "stopped")

	release <- struct{}{}
	e.WaitState(first, "done")
	e.Until("the second to take the folder", func() bool { return len(e.Fake.Calls()) == 2 })
	if c := e.Fake.Calls()[1]; c.Cwd != repo {
		t.Fatalf("the second works in the real path of the folder: %s", c.Cwd)
	}
	fourth := e.Run(a, "run")
	e.WaitState(fourth, "waiting_folder")
	release <- struct{}{}
	e.WaitState(second, "done")
	release <- struct{}{}
	e.WaitState(fourth, "done")
	if n := len(e.Fake.Calls()); n != 3 {
		t.Fatalf("the stopped one never ran: %d calls", n)
	}
}

// TestFolderLockSurvivesARestart: the lock is in the base, so after a restart the agent that had the folder
// goes on in it and the one that waited still waits until it finishes.
func TestFolderLockSurvivesARestart(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	hang := make(chan struct{})
	first := &harness.Fake{Script: held(hang)}
	clock := &harness.Clock{At: harness.T0}
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(first), harness.WithClock(clock))
	p := e.Project("code", harness.TestManifest([]string{harness.FolderCommand("run", "true")}, nil))
	e.Must("deck.folder_add", map[string]any{"project": "code", "path": harness.RepoFolder(t)}, nil)
	a := e.Run(p, "run")
	e.Until("the first at its provider", func() bool { return len(first.Calls()) == 1 })
	b := e.Run(p, "run")
	e.WaitState(b, "waiting_folder")

	release := make(chan struct{}, 4)
	second := &harness.Fake{Script: held(release)}
	e2 := e.Restart(harness.WithProvider(second))
	e2.Resume()
	e2.Until("the one with the folder resumed", func() bool { return len(second.Calls()) == 1 })
	if got := states(e2, []int64{a, b}); got != "running waiting_folder" || len(second.Calls()) != 1 || !second.Calls()[0].Resume {
		t.Fatalf("after a restart the folder stays with its agent: %s, %d calls", got, len(second.Calls()))
	}
	release <- struct{}{}
	e2.WaitState(a, "done")
	e2.WaitState(b, "running", "done")
	release <- struct{}{}
	e2.WaitState(b, "done")
	if n := len(second.Calls()); n != 2 {
		t.Fatalf("each of the two reached its provider once: %d calls", n)
	}
}

// TestOnlyTheOwnerAddsAFolder: a folder lets agents write there, so neither a terminal agent on an automation
// token nor an agent of the project may add one; deck's own folder and a missing one are refused.
func TestOnlyTheOwnerAddsAFolder(t *testing.T) {
	t.Parallel()
	var tools []string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		tools = harness.ToolNames(c.Session)
		out, _ := c.Tool(ctx, "folder_add", map[string]any{"path": "/tmp"})
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`), Text: out}
	}))

	p := e.Project("code", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
	_, tok := e.Token("terminal", "deck.*")
	for _, name := range []string{"deck.folder_add", "deck.folder_remove"} {
		code, out := e.As(tok).Call(name, map[string]any{"project": "code", "path": t.TempDir(), "name": "x"})
		if code != 403 {
			t.Fatalf("%s on an automation token: %d %s", name, code, out)
		}
	}
	id := e.Run(p, "job")
	e.WaitState(id, "done")
	for _, n := range tools {
		if strings.Contains(n, "folder_") {
			t.Fatalf("an agent is offered %s", n)
		}
	}
	var list []struct{ Folders []struct{ Name string } }
	e.ProjectsOf(&list)
	if len(list[0].Folders) != 0 {
		t.Fatalf("nobody but the owner added a folder: %+v", list)
	}
	e.Refused("deck.folder_add", map[string]any{"project": "code", "path": "relative/path"}, 422, "not an absolute path")
	e.Refused("deck.folder_add", map[string]any{"project": "code", "path": filepath.Join(t.TempDir(), "missing")}, 422, "does not exist")
	e.Refused("deck.folder_add", map[string]any{"project": "code", "path": e.App.Cfg.Home}, 422, "deck's own folder")
	e.Refused("deck.folder_add", map[string]any{"project": "code", "path": filepath.Dir(e.App.Cfg.Home)}, 422, "deck's own folder")
	e.Refused("deck.folder_add", map[string]any{"project": "code", "path": t.TempDir(), "name": "Bad Name"}, 422, "folder name")
}

// TestCodexAgentInTheProjectFolderMayWriteItsGit: the profile of a Codex turn in a repo folder writes the folder
// and its .git; a .git file or link names a folder elsewhere the agent may have written, so it gets nothing.
func TestCodexAgentInTheProjectFolderMayWriteItsGit(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	c.Stream(0, "ok.jsonl")
	e := harness.New(t, harness.WithCodex(c))
	home := e.Home
	p := e.Project("paint", strings.Replace(harness.ColorManifest, `"executor": {"type": "agent",`, `"executor": {"type": "agent", "folder": "main",`, 1))
	repo := harness.RepoFolder(t)
	e.Must("deck.folder_add", map[string]any{"project": "paint", "path": repo, "name": "main"}, nil)
	id := e.Run(p, "work")
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("want done: %+v", got)
	}
	cc := c.Call(1)
	fs, _ := cc.Config("permissions.deck.filesystem")
	var cwd string
	_ = json.Unmarshal(cc.Request("thread/start")["cwd"], &cwd)
	for _, want := range []string{harness.QuoteTOML(repo) + `="write"`, harness.QuoteTOML(filepath.Join(repo, ".git")) + `="write"`} {
		if !strings.Contains(fs, want) || cc.Cwd != repo || cwd != repo {
			t.Fatalf("the turn runs in the repo and writes %s: cwd %s thread %s, %s", want, cc.Cwd, cwd, fs)
		}
	}

	tree := filepath.Join(filepath.Dir(repo), "tree")
	if err := errors.Join(os.MkdirAll(tree, 0o700), os.WriteFile(filepath.Join(tree, ".git"), []byte("gitdir: "+home+"\n"), 0o600)); err != nil {
		t.Fatal(err)
	}
	e.Must("deck.folder_remove", map[string]any{"project": "paint", "name": "main"}, nil)
	e.Must("deck.folder_add", map[string]any{"project": "paint", "path": tree, "name": "main"}, nil)
	id = e.Run(p, "work")
	e.WaitState(id, "done", "error")
	fs, _ = c.Call(2).Config("permissions.deck.filesystem")
	if !strings.Contains(fs, harness.QuoteTOML(tree)+`="write"`) || !strings.Contains(fs, harness.QuoteTOML(deckhome.Layout(home).State())+`="deny"`) || strings.Contains(fs, harness.QuoteTOML(home)+`="write"`) {
		t.Fatalf("a .git file grants nothing: %s", fs)
	}
}

// TestTheProjectsTabCreatesAProjectAndGivesItAFolder: the owner creates a project from its name and md prompt
// alone, and the folder forms offer the projects.
func TestTheProjectsTabCreatesAProjectAndGivesItAFolder(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	for _, title := range []string{"Create module", "Import module"} {
		e.SettingsBlock(title, nil)
	}
	e.Must("deck.project_create", map[string]any{"name": "notes", "prompt": "Notes of the owner.\n"}, nil)
	var list []struct{ Name, Status, Description string }
	e.ProjectsOf(&list)
	if len(list) != 1 || list[0].Status != "enabled" || list[0].Description != "Notes of the owner." {
		t.Fatalf("a project from a name and a prompt: %+v", list)
	}
	f := harness.BlockData[struct {
		Values   map[string]any
		Readonly []string
	}](t, e.ScreenForm("notes", "settings", "deck.folder_add")["value"])
	if f.Values["project"] != "notes" || !slices.Contains(f.Readonly, "project") {
		t.Fatalf("adding a folder on the settings of a project works on that project: %+v", f)
	}
}

// TestAPauseTheOwnerStopsFreesTheFolder: a process the owner stops on its pause on a usage limit leaves the
// folder, and the one that waited for it, started on the next model, runs.
func TestAPauseTheOwnerStopsFreesTheFolder(t *testing.T) {
	t.Parallel()
	main := &harness.Fake{Script: func(context.Context, *harness.Call) provider.Result { return harness.Limited(time.Time{}) }}
	other := &harness.Fake{ProviderName: "other", Script: func(context.Context, *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}}
	clock := &harness.Clock{At: harness.T0}
	e := harness.New(t, harness.WithProvider(main, other), harness.WithClock(clock))
	p := e.Project("code", harness.TestManifest([]string{harness.FolderCommand("run", "true")}, nil))
	e.UseModels("code.run", "fake")
	e.Must("deck.folder_add", map[string]any{"project": "code", "path": harness.RepoFolder(t)}, nil)
	a := e.Run(p, "run")
	e.WaitState(a, "rate_limited")
	e.UseModels("code.run", "fake", "other")
	b := e.Run(p, "run")
	e.WaitState(b, "waiting_folder")
	e.Must("deck.process_stop", map[string]any{"process": a}, nil)
	e.WaitState(a, "stopped")
	e.WaitState(b, "done")
}

// TestAStoppedAgentKeepsTheFolderUntilItsTurnExits: a stopped agent whose turn takes a while to die keeps the
// folder, so the next one does not reach its provider in it until then, and it is not sent back meanwhile.
func TestAStoppedAgentKeepsTheFolderUntilItsTurnExits(t *testing.T) {
	t.Parallel()
	dying, inside := make(chan struct{}), make(chan struct{})
	var once sync.Once
	let := func() { once.Do(func() { close(dying) }) }
	var early atomic.Bool
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.N == 1 {
			close(inside)
			<-ctx.Done()
			<-dying
		}
		select {
		case <-dying:
		default:
			early.Store(true)
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))

	t.Cleanup(let)
	p := e.Project("code", harness.TestManifest([]string{harness.FolderCommand("run", "true")}, nil))
	e.Must("deck.folder_add", map[string]any{"project": "code", "path": harness.RepoFolder(t)}, nil)
	first := e.Run(p, "run")
	<-inside
	second := e.Run(p, "run")
	e.WaitState(second, "waiting_folder")

	e.Must("deck.process_stop", map[string]any{"process": first}, nil)
	e.WaitState(first, "stopped")
	e.Refused("deck.process_rework", map[string]any{"process": first, "remark": "again"}, 409, "still stopping")
	if s := e.Proc(second).State; s != "waiting_folder" {
		t.Fatalf("the folder waits for the stopped turn to exit: %s", s)
	}
	let()
	e.WaitState(second, "done")
	if early.Load() {
		t.Fatal("the next agent reached its provider in the folder before the stopped turn exited")
	}
}
