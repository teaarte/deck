package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// codeModule is a copy of examples/code whose GitLab is at url.
func codeModule(t *testing.T, url string) string {
	t.Helper()
	src, _ := filepath.Abs("../../examples/code")
	dst := filepath.Join(t.TempDir(), "code")
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "manifest.json" && url != "" {
			b = []byte(strings.ReplaceAll(string(b), "https://gitlab.com", url))
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dst, rel)), 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func (e *Env) importEnabled(dir, name string) int64 {
	e.T.Helper()
	var p struct{ ID int64 }
	e.must("deck.project_import", map[string]any{"folder": dir, "name": name}, &p)
	var m struct{ Revision int64 }
	e.must("deck.manifest_get", map[string]any{"project": name}, &m)
	e.must("deck.project_enable", map[string]any{"project": name, "revision": m.Revision}, nil)
	e.names[p.ID] = name
	return p.ID
}

type gitlabIssue struct {
	IID         int      `json:"iid"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	WebURL      string   `json:"web_url"`
	CreatedAt   string   `json:"created_at"`
	Labels      []string `json:"labels"`
}

// TestGitLabIssuesBecomeCardsOnce: the example's intake follows a GitLab project through its HTTP tool with the
// token as a deck secret; each open issue becomes one card in Backlog, and polls again take only new ones.
func TestGitLabIssuesBecomeCardsOnce(t *testing.T) {
	var mu sync.Mutex
	issues := []gitlabIssue{
		{IID: 1, Title: "Login button is off", Description: "It sits too low.", CreatedAt: "2026-09-28T10:00:00.000Z", Labels: []string{"ui"}},
		{IID: 2, Title: "Crash on save", CreatedAt: "2026-09-28T11:00:00.000Z"},
	}
	closeAfterFirstPage := 0
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if r.Header.Get("PRIVATE-TOKEN") != "glpat-secret" {
			http.Error(w, `{"message": "401 Unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if r.URL.EscapedPath() != "/api/v4/projects/team%2Fapp/issues" || r.URL.Query().Get("state") != "opened" {
			http.NotFound(w, r)
			return
		}
		after := r.URL.Query().Get("created_after")
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		mu.Lock()
		var open []gitlabIssue
		for _, i := range issues {
			if i.CreatedAt >= after {
				i.WebURL = "https://gitlab.example/team/app/-/issues/" + itoa(int64(i.IID))
				open = append(open, i)
			}
		}
		out := open[min((page-1)*50, len(open)):min(page*50, len(open))]
		if closeAfterFirstPage != 0 && page == 1 {
			issues = slices.DeleteFunc(issues, func(i gitlabIssue) bool { return i.IID == closeAfterFirstPage })
			closeAfterFirstPage = 0
		}
		mu.Unlock()
		b, _ := json.Marshal(out)
		jsonReply(w, string(b))
	})
	clock := &Clock{now: time.Now()}
	e := newEnv(t, ok, withClock(clock))
	e.Clock = clock
	e.must("deck.secret_set", map[string]any{"name": "gitlab", "value": "glpat-secret"}, nil)
	e.importEnabled(codeModule(t, svc.URL), "code")
	if got := e.waitScript(e.start("code", "follow_gitlab", map[string]any{"project": "team/app"}), "done", "error"); got.State != "done" {
		t.Fatalf("follow: %+v", got)
	}
	poll := func() proc {
		t.Helper()
		got := e.waitScript(e.start("code", "poll_gitlab", nil), "done", "error")
		if got.State != "done" {
			t.Fatalf("poll: %+v", got)
		}
		return got
	}
	board := func() []string {
		t.Helper()
		var b struct {
			Lanes []struct {
				Name  string
				Cards []struct{ Title, Text string }
			}
		}
		e.must("deck.cards_list", map[string]any{"project": "code"}, &b)
		var out []string
		for _, l := range b.Lanes {
			for _, c := range l.Cards {
				out = append(out, l.Name+": "+c.Title+" | "+c.Text)
			}
		}
		return out
	}
	clock.Add(16 * time.Minute)
	e.App.Scheduler.Tick(context.Background())
	var first procFull
	e.waitFor("the poll on its schedule", func() bool {
		for _, p := range e.processes() {
			if p.Command == "poll_gitlab" && p.Trigger != 0 && (p.State == "done" || p.State == "error") {
				first = p
				return true
			}
		}
		return false
	})
	cards := board()
	if len(cards) != 2 || !strings.HasPrefix(cards[0], "backlog: #1 Login button is off | It sits too low.") ||
		!strings.Contains(cards[0], "GitLab: https://gitlab.example/team/app/-/issues/1") || !strings.Contains(cards[0], "Labels: ui") ||
		!strings.HasPrefix(cards[1], "backlog: #2 Crash on save") {
		t.Fatalf("each open issue becomes a card in Backlog: %v (%s)", cards, first.Result)
	}
	if got := poll(); string(got.Result) != "null" || len(board()) != 2 {
		t.Fatalf("a second poll takes nothing again: %s %v", got.Result, board())
	}
	mu.Lock()
	issues = append(issues, gitlabIssue{IID: 3, Title: "Same second", CreatedAt: "2026-09-28T11:00:00.000Z"},
		gitlabIssue{IID: 4, Title: "Later", CreatedAt: "2026-09-28T12:00:00.000Z"})
	mu.Unlock()
	poll()
	if cards := board(); len(cards) != 4 || !strings.Contains(cards[2], "#3 Same second") || !strings.Contains(cards[3], "#4 Later") {
		t.Fatalf("new issues, one created in the same second as the last taken, come once: %v", cards)
	}
	if got := poll(); string(got.Result) != "null" {
		t.Fatalf("nothing new: %s", got.Result)
	}
	mu.Lock()
	for n := 5; n < 70; n++ {
		issues = append(issues, gitlabIssue{IID: n, Title: "Bulk " + itoa(int64(n)), CreatedAt: fmt.Sprintf("2026-09-28T%02d:%02d:00.000Z", 13+(n-5)/60, (n-5)%60)})
	}
	closeAfterFirstPage = 5
	mu.Unlock()
	poll()
	if n := len(board()); n != 4+65 {
		t.Fatalf("an issue closed between pages shifts none past the poll: %d cards", n)
	}
	for _, r := range svc.requests() {
		if !strings.Contains(r.Path, "labels=&") || !strings.Contains(r.Path, "per_page=50") {
			t.Fatalf("the tool asks for open issues of every label, oldest first: %s", r.Path)
		}
	}
	var recs []struct{ Data map[string]any }
	e.must("deck.records_list", map[string]any{"project": "code", "collection": "intake"}, &recs)
	if len(recs) != 1 || recs[0].Data["cursor"] != "2026-09-28T14:04:00.000Z" {
		t.Fatalf("the cursor is in the record: %+v", recs)
	}
	var leaked int
	_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM process WHERE result LIKE '%glpat%' OR error LIKE '%glpat%'`).Scan(&leaked)
	if leaked != 0 {
		t.Fatal("the token stays in deck")
	}
}

// TestTheCodeExampleImportsWithItsBoardAndRunButton: the example imports as it is, its board screen has the Run
// button and the board, and its run command waits for the owner's folder.
func TestTheCodeExampleImportsWithItsBoardAndRunButton(t *testing.T) {
	e := newEnv(t, ok)
	check := newCatalogCheck(t)
	p := e.importEnabled(codeModule(t, ""), "code")
	msgs := e.screenMessages("code", "board", nil)
	check.messages(t, msgs)
	raw, _ := json.Marshal(msgs)
	for _, want := range []string{`"Run"`, `"Merge"`, `"Revert the merge"`, `"Show the diff"`, `"Push"`, `"component":"board"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("the board screen has %s", want)
		}
	}
	e.refused("code.run", map[string]any{"do": "run"}, 422, "has no folder yet")
	e.must("deck.folder_add", map[string]any{"project": "code", "path": repoFolder(t)}, nil)
	id := e.run(p, "run")
	e.waitState(id, "done")
	c := e.Fake.Calls()[0]
	if !c.Repo || !strings.Contains(c.Prompt, "as deck process "+itoa(id)) || !strings.Contains(c.Prompt, "git stash push -u -m") {
		t.Fatalf("the run works in the repo with the md of the module and knows its process: repo %v", c.Repo)
	}
}
