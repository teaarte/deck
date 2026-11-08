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
	DurationMs int64 `json:"duration_ms"`
}

type episodes struct {
	Processes []episodeProcess
	Incidents []struct {
		Process int64
		Kind    string
		Project string
	}
	Messages []struct {
		Topic int64
		Text  string
	}
	Summaries []struct {
		Topic   int64
		Summary string
	}
	More []string
}

func (v episodes) has(id int64) bool {
	return slices.ContainsFunc(v.Processes, func(p episodeProcess) bool {
		return p.ID == id
	})
}

// TestEpisodesListWhatHappenedInAPeriod: the owner and the master read the failed processes, incidents, the owner's
// messages and changed summaries of a period; an agent of a module is refused; since and until bound the period.
func TestEpisodesListWhatHappenedInAPeriod(t *testing.T) {
	t.Parallel()
	since := time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)
	var refused atomic.Value
	var read atomic.Value
	long := strings.Repeat("the page was empty; ", 40)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch {
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
	old := store.Time(time.Now().Add(-48 * time.Hour))
	var oldID int64
	// A failure and its incident of two days ago.
	err := e.SQLRow(`INSERT INTO process (project_id, command, kind, state, error, error_type, started_at, finished_at)
		VALUES (?, 'work', 'agent', 'error', 'an old failure', 'task.failed', ?, ?) RETURNING id`, plain, old, old).Scan(&oldID)
	if err != nil {
		t.Fatal(err)
	}
	// The mark deck leaves when it hands an incident to the master.
	e.SQL(`INSERT INTO mark (workspace_id, project_id, name, value) VALUES (1, ?, ?, ?)`, plain, fmt.Sprintf("incident:%d.running", oldID), old)

	id := e.Run(plain, "work")
	e.WaitState(id, "error")
	if r, _ := refused.Load().(string); !strings.HasPrefix(r, "true") {
		t.Fatalf("an agent of a module is refused the episodes: %s", r)
	}
	// The mark of an incident of today.
	e.SQL(`INSERT INTO mark (workspace_id, project_id, name, value) VALUES (1, ?, ?, ?)`, plain, fmt.Sprintf("incident:%d.not_done", id), store.Time(time.Now()))
	topic := e.Say(0, "hello, what broke today")
	// The summary a turn of the master writes.
	e.PlantRevision("master", fmt.Sprintf("topics/%d/summary.md", topic), "The owner asks what broke today.")

	var v episodes
	e.Must("deck.episodes_list", map[string]any{"since": since}, &v)
	i := slices.IndexFunc(v.Processes, func(p episodeProcess) bool { return p.ID == id })
	if i < 0 || v.has(oldID) {
		t.Fatalf("the period holds the failure of today, not the old one: %+v", v.Processes)
	}
	if p := v.Processes[i]; p.Project != "plain" || p.Command != "work" || p.State != "error" || p.ErrorType != "task.failed" ||
		len(p.Error) > 310 || !strings.Contains(p.Error, "the page was empty") || p.Provider == "" || p.DurationMs < 0 {
		t.Fatalf("a process says how it ended, its error cut: %+v", p)
	}
	if len(v.Incidents) != 1 || v.Incidents[0].Process != id || v.Incidents[0].Kind != "not_done" || v.Incidents[0].Project != "plain" {
		t.Fatalf("the incident of today: %+v", v.Incidents)
	}
	if len(v.Messages) != 1 || v.Messages[0].Topic != topic || v.Messages[0].Text != "hello, what broke today" {
		t.Fatalf("the owner's message with its topic: %+v", v.Messages)
	}
	if len(v.Summaries) != 1 || v.Summaries[0].Topic != topic || v.Summaries[0].Summary != "The owner asks what broke today." {
		t.Fatalf("the summary that changed: %+v", v.Summaries)
	}

	before := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	var past episodes
	e.Must("deck.episodes_list", map[string]any{"since": before, "until": since}, &past)
	if !past.has(oldID) || past.has(id) || len(past.Incidents) != 1 || past.Incidents[0].Kind != "running" || len(past.Messages) != 0 {
		t.Fatalf("until ends the period before today: %+v", past)
	}
	var cut episodes
	e.Must("deck.episodes_list", map[string]any{"since": before, "limit": 1}, &cut)
	if len(cut.Processes) != 1 || !slices.Contains(cut.More, "processes") || !slices.Contains(cut.More, "incidents") || slices.Contains(cut.More, "summaries") {
		t.Fatalf("a part cut at the limit says so: %d processes, more %v", len(cut.Processes), cut.More)
	}
	e.Refused("deck.episodes_list", map[string]any{"since": "yesterday"}, 422, "")

	e.Say(topic, "read the episodes of today")
	e.Until("the master reads the episodes", func() bool {
		raw, _ := read.Load().(string)
		var m episodes
		return json.Unmarshal([]byte(raw), &m) == nil && m.has(id) && len(m.Messages) >= 1
	})
}
