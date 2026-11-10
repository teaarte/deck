package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/fault"
	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/tests/harness"
)

type episodeProcess struct {
	ID         int64
	Project    string
	Command    string
	State      string
	ErrorType  string `json:"error_type"`
	Error      string
	Provider   string
	DurationMs int64  `json:"duration_ms"`
	FinishedAt string `json:"finished_at"`
}

type episodes struct {
	Processes []episodeProcess
	Incidents []struct {
		Process int64
		Kind    string
		Project string
	}
	Messages []struct {
		Topic      int64
		TopicTitle string `json:"topic_title"`
		Text       string
	}
	Summaries []struct {
		Topic   int64
		Summary string
	}
	More []string
}

func (v episodes) has(id int64) bool {
	return slices.ContainsFunc(v.Processes, func(p episodeProcess) bool { return p.ID == id })
}

func (v episodes) ids() []int64 {
	var out []int64
	for _, p := range v.Processes {
		out = append(out, p.ID)
	}
	return out
}

// TestEpisodesListWhatHappenedInAPeriod: the owner and the master read what happened in a period, oldest first, bound
// by since, until and project, a cut part going on from its last time; a module's agent and a token are refused.
func TestEpisodesListWhatHappenedInAPeriod(t *testing.T) {
	t.Parallel()
	since := time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)
	var refused, read atomic.Value
	var idle, spare atomic.Int64
	long := strings.Repeat("the page was empty; ", 40)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch {
		case c.Command() == "work" && c.Req.Project == idle.Load():
			return provider.Result{Structured: json.RawMessage(`null`)}
		case c.Command() == "work" && c.Req.Project == spare.Load():
			return provider.Result{Text: "done"}
		case c.Command() == "work":
			out, bad := c.Tool(ctx, "episodes_list", map[string]any{"since": since})
			refused.Store(fmt.Sprint(bad, " ", out))
			return provider.Result{Fault: fault.New(fault.TaskFailed, long)}
		case strings.Contains(c.Req.Prompt, "read the episodes"):
			out, bad := c.Tool(ctx, "episodes_list", map[string]any{"since": since})
			if !bad {
				read.Store(out)
			}
		}
		return provider.Result{Text: "ok"}
	}))

	plain := e.Project("plain", harness.PlainManifest)
	idle.Store(e.Project("idle", harness.Nullable(harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil))))
	spare.Store(e.Project("spare", harness.PlainManifest))
	old := store.Time(time.Now().Add(-48 * time.Hour))
	var oldID int64
	// A failure and its incident of two days ago.
	err := e.SQLRow(`INSERT INTO process (project_id, command, kind, state, error, error_type, started_at, finished_at)
		VALUES (?, 'work', 'agent', 'error', 'an old failure', 'task.failed', ?, ?) RETURNING id`, plain, old, old).Scan(&oldID)
	if err != nil {
		t.Fatal(err)
	}
	// The mark deck leaves when it hands an incident to the master.
	e.SQL(`INSERT INTO mark (workspace_id, project_id, name, value) VALUES (1, ?, ?, ?)`, plain, fmt.Sprintf("processes:incident:%d.running", oldID), old)

	id := e.Run(plain, "work")
	e.WaitState(id, "error")
	if r, _ := refused.Load().(string); !strings.HasPrefix(r, "true") {
		t.Fatalf("an agent of a module is refused the episodes: %s", r)
	}
	nothing := e.Run(idle.Load(), "work")
	e.WaitState(nothing, "done")
	other := e.Run(spare.Load(), "work")
	e.WaitState(other, "done")
	// The marks of incidents of today.
	e.SQL(`INSERT INTO mark (workspace_id, project_id, name, value) VALUES (1, ?, ?, ?)`, plain, fmt.Sprintf("processes:incident:%d.not_done", id), store.Time(time.Now()))
	// The same.
	e.SQL(`INSERT INTO mark (workspace_id, project_id, name, value) VALUES (1, ?, ?, ?)`, spare.Load(), fmt.Sprintf("processes:incident:%d.running", other), store.Time(time.Now().Add(time.Millisecond)))
	title := "hello, what broke today" + strings.Repeat(" and why", 30)
	topic := e.Say(0, title)
	// The summary a turn of the master writes.
	e.PlantRevision("master", fmt.Sprintf("topics/%d/summary.md", topic), "The owner asks what broke today.")
	gone := e.Say(0, "a topic to forget")
	// The same.
	e.PlantRevision("master", fmt.Sprintf("topics/%d/summary.md", gone), "Forgotten.")
	e.Must("deck.chat_topic_delete", map[string]any{"topic": gone}, nil)

	var v episodes
	e.Must("deck.episodes_list", map[string]any{"since": since}, &v)
	i := slices.IndexFunc(v.Processes, func(p episodeProcess) bool { return p.ID == id })
	if i < 0 || v.has(oldID) || v.has(nothing) || !v.has(other) {
		t.Fatalf("the period holds what ended today with something, not the old one nor one done with nothing: %+v", v.Processes)
	}
	if p := v.Processes[i]; p.Project != "plain" || p.Command != "work" || p.State != "error" || p.ErrorType != "task.failed" ||
		len(p.Error) > 310 || !strings.Contains(p.Error, "the page was empty") || p.Provider == "" || p.DurationMs < 0 {
		t.Fatalf("a process says how it ended, its error cut: %+v", p)
	}
	if !slices.IsSortedFunc(v.Processes, func(a, b episodeProcess) int { return strings.Compare(a.FinishedAt, b.FinishedAt) }) {
		t.Fatalf("the oldest first: %+v", v.Processes)
	}
	if len(v.Incidents) != 2 || v.Incidents[0].Process != id || v.Incidents[0].Kind != "not_done" || v.Incidents[0].Project != "plain" ||
		v.Incidents[1].Process != other {
		t.Fatalf("the incidents of today, oldest first: %+v", v.Incidents)
	}
	if len(v.Messages) != 1 || v.Messages[0].Topic != topic || v.Messages[0].Text != title || len(v.Messages[0].TopicTitle) > 130 {
		t.Fatalf("the owner's message with its topic, its title cut, not those of a deleted topic: %+v", v.Messages)
	}
	if len(v.Summaries) != 1 || v.Summaries[0].Topic != topic || v.Summaries[0].Summary != "The owner asks what broke today." {
		t.Fatalf("the summary that changed, not that of a deleted topic: %+v", v.Summaries)
	}

	var mine episodes
	e.Must("deck.episodes_list", map[string]any{"since": since, "project": "plain"}, &mine)
	if !slices.Equal(mine.ids(), []int64{id}) || len(mine.Incidents) != 1 || mine.Incidents[0].Process != id || len(mine.Messages) != 1 {
		t.Fatalf("project narrows the processes and incidents, the chat stays: %+v", mine)
	}

	before := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	var past episodes
	e.Must("deck.episodes_list", map[string]any{"since": before, "until": since}, &past)
	if !past.has(oldID) || past.has(id) || len(past.Incidents) != 1 || past.Incidents[0].Kind != "running" || len(past.Messages) != 0 {
		t.Fatalf("until ends the period before today: %+v", past)
	}

	var all, first, rest episodes
	e.Must("deck.episodes_list", map[string]any{"since": before}, &all)
	e.Must("deck.episodes_list", map[string]any{"since": before, "limit": 2}, &first)
	if !slices.Equal(first.ids(), all.ids()[:2]) || first.ids()[0] != oldID || !slices.Contains(first.More, "processes") ||
		slices.Contains(first.More, "summaries") {
		t.Fatalf("a part cut at the limit gives the oldest and says so: %v of %v, more %v", first.ids(), all.ids(), first.More)
	}
	e.Must("deck.episodes_list", map[string]any{"since": first.Processes[1].FinishedAt}, &rest)
	got := slices.Concat(first.ids(), rest.ids())
	if slices.ContainsFunc(all.ids(), func(id int64) bool { return !slices.Contains(got, id) }) {
		t.Fatalf("since the last time of a page gives the rest: %v and %v of %v", first.ids(), rest.ids(), all.ids())
	}

	e.Refused("deck.episodes_list", map[string]any{"since": "yesterday"}, 422, "")
	_, tok := e.Token("terminal", "deck.episodes_list", "deck.*")
	e.As(tok).Refused("deck.episodes_list", map[string]any{"since": since}, 403, "not open to automation tokens")

	e.Say(topic, "read the episodes of today")
	e.Until("the master reads the episodes", func() bool {
		raw, _ := read.Load().(string)
		var m episodes
		return json.Unmarshal([]byte(raw), &m) == nil && m.has(id) && len(m.Messages) >= 1
	})
}
