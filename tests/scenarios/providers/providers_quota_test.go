package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/tests/harness"
)

// quotaReset is when the windows of the streams of these tests reset.
const quotaReset = 1790379000

// withQuota is the stream of an answered turn of Claude Code whose rate_limit_event tells the windows used, a status.
func withQuota(t *testing.T, status string, fiveHour, week float64) string {
	t.Helper()
	b, err := os.ReadFile(harness.Testdata("claude/ok.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	ls := strings.Split(strings.TrimSpace(string(b)), "\n")
	ls[2] = fmt.Sprintf(`{"type":"rate_limit_event","rate_limit_info":{"status":%q,"resetsAt":%d,"rateLimitType":"five_hour","unifiedWindows":{"five_hour":{"utilization":%g,"resetsAt":%d},"seven_day":{"utilization":%g,"resetsAt":%d}}},"session_id":"{{session}}"}`,
		status, quotaReset, fiveHour, quotaReset, week, quotaReset+5*86400)
	return strings.Join(ls, "\n") + "\n"
}

// TestTheQuotaOfASubscriptionShowsAndANearlySpentOneHandsNewTurnsOn: the windows a turn of a CLI tells show in the
// row of its subscription and in Settings → Models; at 98 % new turns go to the next model until the window resets.
func TestTheQuotaOfASubscriptionShowsAndANearlySpentOneHandsNewTurnsOn(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.StreamText(1, withQuota(t, "allowed", 0.62, 0.4))
	c.StreamText(2, withQuota(t, "allowed_warning", 0.98, 0.41))
	backup := &harness.Fake{ProviderName: "backup", Script: func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{}`), Text: "done"}
	}}
	clock := &harness.Clock{At: time.Unix(quotaReset, 0).Add(-2 * time.Hour)}
	e := harness.New(t, harness.WithCLI(c), harness.WithProvider(backup), harness.WithClock(clock))
	e.Project("plain", harness.OnLogin(harness.PlainManifest))
	e.UseModels("plain.work", "claude-code", "backup")
	if got := e.WaitState(e.Start("plain", "work", nil), "done", "error"); got.State != "done" {
		t.Fatalf("the turn is done: %+v", got)
	}
	want := fmt.Sprintf("5 hours: 62 percent used, resets at %s; week: 40 percent used, resets at %s", store.Time(time.Unix(quotaReset, 0)), store.Time(time.Unix(quotaReset+5*86400, 0)))
	if h := e.HealthRowOf("limit:claude-code"); h == nil || h["status"] != "ok" || h["error"] != want {
		t.Fatalf("the row of the subscription shows its windows: %v", h)
	}
	var out struct {
		Models []struct{ Name, Status, Error string }
	}
	e.Must("deck.models_list", map[string]any{}, &out)
	shown := ""
	for _, m := range out.Models {
		if m.Name == "claude-code" {
			shown = m.Error
		}
	}
	if !strings.Contains(shown, "62 percent") {
		t.Fatalf("Settings → Models shows the quota of the CLI: %+v", out.Models)
	}
	if got := e.WaitState(e.Start("plain", "work", nil), "done", "error"); got.State != "done" || len(backup.Calls()) != 0 {
		t.Fatalf("the turn that used 98 %% is done on the CLI: %+v", got)
	}
	if h := e.WaitHealth("limit:claude-code", "paused"); !strings.Contains(h.Error, "next try at "+store.Time(time.Unix(quotaReset, 0))) {
		t.Fatalf("at 98 %% the subscription pauses until its window resets: %+v", h)
	}
	id := e.Start("plain", "work", nil)
	if got := e.WaitState(id, "done", "error"); got.State != "done" || len(backup.Calls()) != 1 || c.Calls() != 2 {
		t.Fatalf("a new turn goes to the next model: %+v, %d calls of the CLI", got, c.Calls())
	}
}

// TestAShortLimitOfASubscriptionWaitsOnTheSameLogin: a limit of a CLI that resets within a minute is waited out in the
// same session, not handed to the next model; a spent window hands on.
func TestAShortLimitOfASubscriptionWaitsOnTheSameLogin(t *testing.T) {
	t.Parallel()
	rejected := func(reset int64) string {
		b, _ := os.ReadFile(harness.Testdata("claude/ratelimit_rejected.jsonl"))
		return strings.ReplaceAll(string(b), "1790379000", fmt.Sprint(reset))
	}
	c := harness.NewCLI(t)
	c.StreamText(1, rejected(quotaReset))
	c.StreamText(2, withQuota(t, "allowed", 0.1, 0.1))
	c.StreamText(3, rejected(quotaReset+5*3600))
	backup := &harness.Fake{ProviderName: "backup", Script: func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{}`), Text: "done"}
	}}
	clock := &harness.Clock{At: time.Unix(quotaReset, 0).Add(-30 * time.Second)}
	e := harness.New(t, harness.WithCLI(c), harness.WithProvider(backup), harness.WithClock(clock))
	e.Project("plain", harness.OnLogin(harness.PlainManifest))
	e.UseModels("plain.work", "claude-code", "backup")
	id := e.Start("plain", "work", nil)
	e.WaitState(id, "waiting:limit")
	e.Clock.Add(30 * time.Second)
	e.Tick()
	if got := e.WaitState(id, "done", "error"); got.State != "done" || len(backup.Calls()) != 0 || c.Calls() != 2 || !strings.Contains(strings.Join(c.Call(2).Args, " "), "--resume") {
		t.Fatalf("a short limit is waited out in the same session: %+v, backup %d", got, len(backup.Calls()))
	}
	id = e.Start("plain", "work", nil)
	if got := e.WaitState(id, "error", "done", "waiting:limit"); got.State != "error" || e.Fault(id).ErrorType != "model.limit" {
		t.Fatalf("a spent window closes the process: %+v", got)
	}
	if got := e.WaitState(e.Following(id), "done", "error"); got.State != "done" || len(backup.Calls()) != 1 {
		t.Fatalf("a spent window hands the work on to the next model: %+v", got)
	}
}
