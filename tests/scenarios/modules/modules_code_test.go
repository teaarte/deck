package modules

import (
	"context"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type gitlabIssue struct {
	IID         int      `json:"iid"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	WebURL      string   `json:"web_url"`
	CreatedAt   string   `json:"created_at"`
	Labels      []string `json:"labels"`
}

// TestGitLabIssuesBecomeCardsOnce: a tracker intake by the code-project skill follows GitLab through an HTTP tool with
// the token as a deck secret; each open issue becomes one task in backlog, and polls again take only new ones.
func TestGitLabIssuesBecomeTasksOnce(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	// The first poll takes the last two weeks: the issues are of two days ago.
	day := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	issues := []gitlabIssue{
		{IID: 1, Title: "Login button is off", Description: "It sits too low.", CreatedAt: day + "T10:00:00.000Z", Labels: []string{"ui"}},
		{IID: 2, Title: "Crash on save", CreatedAt: day + "T11:00:00.000Z"},
	}
	closeAfterFirstPage := 0
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) {
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
				i.WebURL = "https://gitlab.example/team/app/-/issues/" + harness.Itoa(int64(i.IID))
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
		harness.JsonReply(w, string(b))
	})
	clock := &harness.Clock{At: time.Now()}
	e := harness.New(t, harness.WithAgent(harness.Ok), harness.WithClock(clock))
	e.Must("deck.secret_set", map[string]any{"name": "gitlab", "value": "glpat-secret"}, nil)
	e.ImportEnabled(harness.CodeModule(t, svc.URL), "code")
	if got := e.WaitState(e.Start("code", "follow_gitlab", map[string]any{"project": "team/app"}), "done", "error"); got.State != "done" {
		t.Fatalf("follow: %+v", got)
	}
	poll := func() harness.Proc {
		t.Helper()
		got := e.WaitState(e.Start("code", "poll_gitlab", nil), "done", "error")
		if got.State != "done" {
			t.Fatalf("poll: %+v", got)
		}
		return got
	}
	board := func() []string {
		t.Helper()
		var rs []harness.RecordRow
		e.Must("deck.records_list", map[string]any{"project": "code", "collection": "tasks"}, &rs)
		slices.SortFunc(rs, func(a, b harness.RecordRow) int { return int(a.ID - b.ID) })
		var out []string
		for _, r := range rs {
			out = append(out, fmt.Sprint(r.Data["status"])+": "+fmt.Sprint(r.Data["title"])+" | "+fmt.Sprint(r.Data["text"]))
		}
		return out
	}
	clock.Add(16 * time.Minute)
	e.App.Scheduler.Tick(context.Background())
	var first harness.Proc
	e.Until("the poll on its schedule", func() bool {
		for _, p := range e.Procs() {
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
		t.Fatalf("each open issue becomes a task in backlog: %v (%s)", cards, first.Result)
	}
	if got := poll(); string(got.Result) != "null" || len(board()) != 2 {
		t.Fatalf("a second poll takes nothing again: %s %v", got.Result, board())
	}
	mu.Lock()
	issues = append(issues, gitlabIssue{IID: 3, Title: "Same second", CreatedAt: day + "T11:00:00.000Z"},
		gitlabIssue{IID: 4, Title: "Later", CreatedAt: day + "T12:00:00.000Z"})
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
		issues = append(issues, gitlabIssue{IID: n, Title: "Bulk " + harness.Itoa(int64(n)), CreatedAt: fmt.Sprintf("%sT%02d:%02d:00.000Z", day, 13+(n-5)/60, (n-5)%60)})
	}
	closeAfterFirstPage = 5
	mu.Unlock()
	poll()
	if n := len(board()); n != 4+65 {
		t.Fatalf("an issue closed between pages shifts none past the poll: %d tasks", n)
	}
	for _, r := range svc.Requests() {
		if !strings.Contains(r.Path, "labels=&") || !strings.Contains(r.Path, "per_page=50") {
			t.Fatalf("the tool asks for open issues of every label, oldest first: %s", r.Path)
		}
	}
	var recs []struct{ Data map[string]any }
	e.Must("deck.records_list", map[string]any{"project": "code", "collection": "intake"}, &recs)
	if len(recs) != 1 || recs[0].Data["cursor"] != day+"T14:04:00.000Z" {
		t.Fatalf("the cursor is in the record: %+v", recs)
	}
	var leaked int
	_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM process WHERE result LIKE '%glpat%' OR error LIKE '%glpat%'`).Scan(&leaked)
	if leaked != 0 {
		t.Fatal("the token stays in deck")
	}
}

// TestTheCodeExampleImportsWithItsTasksAndRunButton: the example imports as it is, its Tasks screen has the Run
// button and the table of tasks, and its run command waits for the owner's folder.
func TestTheCodeExampleImportsWithItsTasksAndRunButton(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	check := harness.NewCatalogCheck(t)
	p := e.ImportEnabled(harness.CodeModule(t, ""), "code")
	msgs := e.ScreenMessages("code", "tasks", nil)
	check.Messages(t, msgs)
	raw, _ := json.Marshal(msgs)
	for _, want := range []string{`"Run"`, `"Merge"`, `"Revert the merge"`, `"Show the diff"`, `"Push"`, `"component":"table"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("the tasks screen has %s", want)
		}
	}
	e.Refused("code.run", map[string]any{"do": "run"}, 422, "has no folder yet")
	e.Must("deck.folder_add", map[string]any{"project": "code", "path": harness.RepoFolder(t)}, nil)
	id := e.Run(p, "run")
	e.WaitState(id, "done")
	c := e.Fake.Calls()[0]
	if !c.Repo || !strings.Contains(c.Prompt, "as deck process "+harness.Itoa(id)) || !strings.Contains(c.Prompt, "git stash push -u -m") {
		t.Fatalf("the run works in the repo with the md of the module and knows its process: repo %v", c.Repo)
	}
}
