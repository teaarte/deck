package scenarios

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
	"testing"
	"time"

	"deck2/internal/provider"
)

// folderCommand is an agent command of a test manifest that works in a project folder.
func folderCommand(name, folder string) string {
	return strings.Replace(agentCommand(name, ""), `"executor": {"type": "agent",`, `"executor": {"type": "agent", "folder": `+folder+`,`, 1)
}

// repoFolder is a folder that looks like a git repo to deck, by its real path.
func repoFolder(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "My Repo")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// TestAgentCommandWorksInTheProjectFolder: an agent command with folder runs in the project folder the owner
// added, as a repo when it is one, and a rework goes back to the same folder; the Projects tab lists folders.
func TestAgentCommandWorksInTheProjectFolder(t *testing.T) {
	e := newEnv(t, ok)
	p := e.createProject("code", testManifest([]string{folderCommand("run", "true"), agentCommand("plain", ""),
		folderCommand("docs", `"notes"`)}, nil))
	e.refused("code.run", map[string]any{}, 422, "has no folder yet")

	repo := repoFolder(t)
	var added struct{ Name, Path string }
	e.must("deck.folder_add", map[string]any{"project": "code", "path": repo}, &added)
	if added.Name != "my-repo" || added.Path != repo {
		t.Fatalf("a folder gets the name of its own folder and its real path: %+v", added)
	}
	e.refused("deck.folder_add", map[string]any{"project": "code", "path": repo, "name": "again"}, 422, "already has folder")

	id := e.run(p, "run")
	e.waitState(id, "done")
	c := e.Fake.Calls()[0]
	if c.Cwd != repo || !c.Repo || !strings.Contains(c.Prompt, "You work in the project folder "+repo) {
		t.Fatalf("the agent works in the project folder as in a repo: cwd %s repo %v", c.Cwd, c.Repo)
	}
	plain := e.run(p, "plain")
	e.waitState(plain, "done")
	if c := e.Fake.Calls()[1]; c.Cwd == repo || c.Repo || !strings.Contains(c.Cwd, filepath.Join("runs", "")) {
		t.Fatalf("a command without folder keeps a work folder of its own: %s", c.Cwd)
	}
	e.must("deck.process_rework", map[string]any{"process": id, "remark": "once more"}, nil)
	e.waitFor("the rework to run", func() bool { return len(e.Fake.Calls()) == 3 })
	e.waitState(id, "done")
	if c := e.Fake.Calls()[2]; c.Cwd != repo || !c.Resume {
		t.Fatalf("a rework resumes the session in the same folder: %s resume %v", c.Cwd, c.Resume)
	}

	e.refused("code.docs", map[string]any{}, 422, "has no folder notes")
	notes := t.TempDir()
	e.must("deck.folder_add", map[string]any{"project": "code", "path": notes, "name": "notes"}, nil)
	e.refused("code.run", map[string]any{}, 422, "has 2 folders")
	docs := e.run(p, "docs")
	e.waitState(docs, "done")
	if c := e.Fake.Calls()[3]; c.Repo || !strings.HasSuffix(c.Cwd, filepath.Base(notes)) {
		t.Fatalf("a named folder that is no repo: %s repo %v", c.Cwd, c.Repo)
	}

	var list []struct {
		Name    string
		Folders []struct{ Name, Path string }
	}
	e.projectsOf(&list)
	if len(list) != 1 || len(list[0].Folders) != 2 || list[0].Folders[0].Name != "my-repo" {
		t.Fatalf("projects_list gives the folders: %+v", list)
	}
	var items []listItem
	_ = json.Unmarshal(e.settingsBlock("Projects", nil)["items"], &items)
	if len(items) != 2 || !strings.Contains(items[0].Meta, "folders: my-repo: "+repo+"; notes: ") || strings.Contains(fmt.Sprint(items[1].Actions), "project_delete") {
		t.Fatalf("the Projects tab shows the folders: %+v", items)
	}
	e.must("deck.folder_remove", map[string]any{"project": "code", "name": "notes"}, nil)
	e.refused("deck.folder_remove", map[string]any{"project": "code", "name": "notes"}, 404, "no folder notes")
	if _, err := os.Stat(notes); err != nil {
		t.Fatalf("removing a folder leaves it on disk: %v", err)
	}
	again := e.run(p, "run")
	e.waitState(again, "done")

	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	e.refused("code.run", map[string]any{}, 422, "is gone")
}

// TestSecondAgentInTheSameFolderWaitsForTheLock: two projects share a folder, one by a link; the second agent
// waits for it without reaching its provider, says so in the cockpit and runs when the first finishes.
func TestSecondAgentInTheSameFolderWaitsForTheLock(t *testing.T) {
	release := make(chan struct{}, 10)
	e := newEnv(t, held(release))
	repo := repoFolder(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	a := e.createProject("front", testManifest([]string{folderCommand("run", "true")}, nil))
	b := e.createProject("back", testManifest([]string{folderCommand("run", "true")}, nil))
	e.must("deck.folder_add", map[string]any{"project": "front", "path": repo}, nil)
	e.must("deck.folder_add", map[string]any{"project": "back", "path": link, "name": "repo"}, nil)

	first := e.run(a, "run")
	e.waitFor("the first agent at its provider", func() bool { return len(e.Fake.Calls()) == 1 })
	second, third := e.run(b, "run"), e.run(a, "run")
	e.waitFor("the others to wait for the folder", func() bool {
		return e.states([]int64{first, second, third}) == "running waiting_folder waiting_folder"
	})
	time.Sleep(200 * time.Millisecond)
	if n := len(e.Fake.Calls()); n != 1 {
		t.Fatalf("a process waiting for the folder does not reach its provider: %d calls", n)
	}
	waits := 0
	for _, it := range e.cockpitRunning() {
		if it.Text == "waits for folder "+repo {
			waits++
		}
	}
	if waits != 2 {
		t.Fatalf("running now says which wait for the folder: %+v", e.cockpitRunning())
	}
	e.must("deck.process_stop", map[string]any{"process": third}, nil)
	e.waitState(third, "stopped")

	release <- struct{}{}
	e.waitState(first, "done")
	e.waitFor("the second to take the folder", func() bool { return len(e.Fake.Calls()) == 2 })
	if c := e.Fake.Calls()[1]; c.Cwd != repo {
		t.Fatalf("the second works in the real path of the folder: %s", c.Cwd)
	}
	fourth := e.run(a, "run")
	e.waitState(fourth, "waiting_folder")
	release <- struct{}{}
	e.waitState(second, "done")
	release <- struct{}{}
	e.waitState(fourth, "done")
	if n := len(e.Fake.Calls()); n != 3 {
		t.Fatalf("the stopped one never ran: %d calls", n)
	}
}

// TestFolderLockSurvivesARestart: the lock is in the base, so after a restart the agent that had the folder
// goes on in it and the one that waited still waits until it finishes.
func TestFolderLockSurvivesARestart(t *testing.T) {
	home := t.TempDir()
	hang := make(chan struct{})
	first := &Fake{script: held(hang)}
	clock := &Clock{now: t0}
	e := newEnvWith(t, home, first, withClock(clock))
	e.Fake, e.Clock = first, clock
	p := e.createProject("code", testManifest([]string{folderCommand("run", "true")}, nil))
	e.must("deck.folder_add", map[string]any{"project": "code", "path": repoFolder(t)}, nil)
	a := e.run(p, "run")
	e.waitFor("the first at its provider", func() bool { return len(first.Calls()) == 1 })
	b := e.run(p, "run")
	e.waitState(b, "waiting_folder")

	release := make(chan struct{}, 4)
	second := &Fake{script: held(release)}
	e2 := restart(t, e, home, second)
	e2.waitFor("the one with the folder resumed", func() bool { return len(second.Calls()) == 1 })
	time.Sleep(300 * time.Millisecond)
	if got := e2.states([]int64{a, b}); got != "running waiting_folder" || len(second.Calls()) != 1 || !second.Calls()[0].Resume {
		t.Fatalf("after a restart the folder stays with its agent: %s, %d calls", got, len(second.Calls()))
	}
	release <- struct{}{}
	e2.waitState(a, "done")
	e2.waitState(b, "running", "done")
	release <- struct{}{}
	e2.waitState(b, "done")
}

// TestOnlyTheOwnerAddsAFolder: a folder lets agents write there, so neither a terminal agent on an automation
// token nor an agent of the project may add one; deck's own folder and a missing one are refused.
func TestOnlyTheOwnerAddsAFolder(t *testing.T) {
	var tools []string
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		tools = toolNames(c.Session)
		out, _ := c.Tool(ctx, "folder_add", map[string]any{"path": "/tmp"})
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`), Text: out}
	})
	p := e.createProject("code", testManifest([]string{agentCommand("job", "")}, nil))
	_, tok := e.token("terminal", "deck.*")
	for _, name := range []string{"deck.folder_add", "deck.folder_remove"} {
		code, out := e.callWith(tok, name, map[string]any{"project": "code", "path": t.TempDir(), "name": "x"})
		if code != 403 {
			t.Fatalf("%s on an automation token: %d %s", name, code, out)
		}
	}
	id := e.run(p, "job")
	e.waitState(id, "done")
	for _, n := range tools {
		if strings.Contains(n, "folder_") {
			t.Fatalf("an agent is offered %s", n)
		}
	}
	var list []struct{ Folders []struct{ Name string } }
	e.projectsOf(&list)
	if len(list[0].Folders) != 0 {
		t.Fatalf("nobody but the owner added a folder: %+v", list)
	}
	e.refused("deck.folder_add", map[string]any{"project": "code", "path": "relative/path"}, 422, "not an absolute path")
	e.refused("deck.folder_add", map[string]any{"project": "code", "path": filepath.Join(t.TempDir(), "missing")}, 422, "does not exist")
	e.refused("deck.folder_add", map[string]any{"project": "code", "path": e.App.Cfg.Home}, 422, "deck's own folder")
	e.refused("deck.folder_add", map[string]any{"project": "code", "path": filepath.Dir(e.App.Cfg.Home)}, 422, "deck's own folder")
	e.refused("deck.folder_add", map[string]any{"project": "code", "path": t.TempDir(), "name": "Bad Name"}, 422, "folder name")
}

// TestCodexAgentInTheProjectFolderMayWriteItsGit: the profile of a Codex turn in a repo folder writes the folder
// and its .git; a .git file or link names a folder elsewhere the agent may have written, so it gets nothing.
func TestCodexAgentInTheProjectFolderMayWriteItsGit(t *testing.T) {
	c := newCodex(t)
	c.stream(0, "ok.jsonl")
	e, home := codexEnv(t, c)
	p := e.createProject("paint", strings.Replace(colorManifest, `"executor": {"type": "agent",`, `"executor": {"type": "agent", "folder": "main",`, 1))
	repo := repoFolder(t)
	e.must("deck.folder_add", map[string]any{"project": "paint", "path": repo, "name": "main"}, nil)
	id := e.run(p, "work")
	if got := e.waitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("want done: %+v", got)
	}
	cc := c.call(1)
	fs, _ := cc.config("permissions.deck.filesystem")
	var cwd string
	_ = json.Unmarshal(cc.request("thread/start")["cwd"], &cwd)
	for _, want := range []string{quoteTOML(repo) + `="write"`, quoteTOML(filepath.Join(repo, ".git")) + `="write"`} {
		if !strings.Contains(fs, want) || cc.cwd != repo || cwd != repo {
			t.Fatalf("the turn runs in the repo and writes %s: cwd %s thread %s, %s", want, cc.cwd, cwd, fs)
		}
	}

	tree := filepath.Join(filepath.Dir(repo), "tree")
	if err := errors.Join(os.MkdirAll(tree, 0o700), os.WriteFile(filepath.Join(tree, ".git"), []byte("gitdir: "+home+"\n"), 0o600)); err != nil {
		t.Fatal(err)
	}
	e.must("deck.folder_remove", map[string]any{"project": "paint", "name": "main"}, nil)
	e.must("deck.folder_add", map[string]any{"project": "paint", "path": tree, "name": "main"}, nil)
	id = e.run(p, "work")
	e.waitState(id, "done", "error")
	fs, _ = c.call(2).config("permissions.deck.filesystem")
	if !strings.Contains(fs, quoteTOML(tree)+`="write"`) || !strings.Contains(fs, quoteTOML(home)+`="deny"`) || strings.Contains(fs, quoteTOML(home)+`="write"`) {
		t.Fatalf("a .git file grants nothing: %s", fs)
	}
}

// TestAFolderCommandNeedsAProviderThatWorksInFolders: a provider that cannot work in the owner's folder yet
// does not start a command with folder.
func TestAFolderCommandNeedsAProviderThatWorksInFolders(t *testing.T) {
	e := newEnv(t, ok)
	e.Fake.noFolder = true
	e.createProject("code", testManifest([]string{folderCommand("run", "true")}, nil))
	e.must("deck.folder_add", map[string]any{"project": "code", "path": repoFolder(t)}, nil)
	e.refused("code.run", map[string]any{}, 422, "cannot yet")
}

// TestTheProjectsTabCreatesAProjectAndGivesItAFolder: the owner creates a project from its name and md prompt
// alone, and the folder forms offer the projects.
func TestTheProjectsTabCreatesAProjectAndGivesItAFolder(t *testing.T) {
	e := newEnv(t, ok)
	for _, title := range []string{"Create project", "Import project"} {
		e.settingsBlock(title, nil)
	}
	e.must("deck.project_create", map[string]any{"name": "notes", "prompt": "Notes of the owner.\n"}, nil)
	var list []struct{ Name, Status, Description string }
	e.projectsOf(&list)
	if len(list) != 1 || list[0].Status != "enabled" || list[0].Description != "Notes of the owner." {
		t.Fatalf("a project from a name and a prompt: %+v", list)
	}
	f := blockData[struct {
		Values   map[string]any
		Readonly []string
	}](t, e.screenForm("notes", "settings", "deck.folder_add")["value"])
	if f.Values["project"] != "notes" || !slices.Contains(f.Readonly, "project") {
		t.Fatalf("adding a folder on the settings of a project works on that project: %+v", f)
	}
	var board struct{ Lanes []struct{ Name string } }
	e.must("deck.cards_list", map[string]any{"project": "notes"}, &board)
	if len(board.Lanes) == 0 {
		t.Fatalf("the new project has the base board")
	}
}

// TestAPauseThatRunsOutFreesTheFolder: a process stopped at the end of its pause on a usage limit leaves the
// folder, and the one that waited for it runs.
func TestAPauseThatRunsOutFreesTheFolder(t *testing.T) {
	var first string
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if first == "" || first == c.Req.SessionID {
			first = c.Req.SessionID
			return limited(time.Time{})
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	p := e.createProject("code", testManifest([]string{folderCommand("run", "true")}, nil))
	e.must("deck.folder_add", map[string]any{"project": "code", "path": repoFolder(t)}, nil)
	a := e.run(p, "run")
	e.waitState(a, "rate_limited")
	b := e.run(p, "run")
	e.waitState(b, "waiting_folder")
	for range 7 {
		e.Clock.Add(time.Hour)
		e.sweep()
		e.settle()
	}
	e.waitState(a, "stopped")
	e.waitState(b, "done")
}

// TestAStoppedAgentKeepsTheFolderUntilItsTurnExits: a stopped agent whose turn takes a while to die keeps the
// folder, so the next one does not reach its provider in it until then, and it is not sent back meanwhile.
func TestAStoppedAgentKeepsTheFolderUntilItsTurnExits(t *testing.T) {
	dying, inside := make(chan struct{}), make(chan struct{})
	var once sync.Once
	let := func() { once.Do(func() { close(dying) }) }
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.N == 1 {
			close(inside)
			<-ctx.Done()
			<-dying
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	t.Cleanup(let)
	p := e.createProject("code", testManifest([]string{folderCommand("run", "true")}, nil))
	e.must("deck.folder_add", map[string]any{"project": "code", "path": repoFolder(t)}, nil)
	first := e.run(p, "run")
	<-inside
	second := e.run(p, "run")
	e.waitState(second, "waiting_folder")

	e.must("deck.process_stop", map[string]any{"process": first}, nil)
	e.waitState(first, "stopped")
	e.refused("deck.process_rework", map[string]any{"process": first, "remark": "again"}, 409, "still stopping")
	time.Sleep(300 * time.Millisecond)
	if n := len(e.Fake.Calls()); n != 1 || e.process(second).State != "waiting_folder" {
		t.Fatalf("the folder waits for the stopped turn to exit: %d calls, %s", n, e.process(second).State)
	}
	let()
	e.waitState(second, "done")
}
