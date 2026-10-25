package screens

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
)

type todayIn struct {
	Lang string `opt:"" desc:"Language of the lines, such as ru; English without one"`
}

// todayLine is a line of the card of Today, the most pressing first: what waits, what failed, what runs, what the day
// did and cost and what runs next; a calm day says so first.
type todayLine struct {
	ID    string `json:"id"`
	Kind  string `json:"kind" enum:"asks|failures|running|calm|day|spent|next"`
	Title string `json:"title"`
	Text  string `json:"text,omitempty"`
	Icon  string `json:"icon"`
	Tone  string `json:"tone,omitempty"`
	Since string `json:"since,omitempty"`
}

// today are the lines of the card of Today, read by the reads of deck as the caller.
func (ss *Service) today(ctx context.Context, act registry.Actor, in todayIn) ([]todayLine, error) {
	ws := Phrases(in.Lang)
	tr := func(s string, a ...any) string { return fmt.Sprintf(manifest.TrText(ws, s), a...) }
	out := []todayLine{}
	var asks []struct{ What, Allow string }
	if err := ss.read(ctx, act, "asks_list", map[string]any{"lang": in.Lang}, &asks); err != nil {
		return nil, err
	}
	if n := len(asks); n > 0 {
		title := tr("One decision waits")
		if n > 1 {
			title = tr("Decisions wait: %d", n)
		}
		out = append(out, todayLine{ID: "asks", Kind: "asks", Title: title, Text: cmp.Or(asks[0].Allow, asks[0].What), Icon: "hand-finger", Tone: "warn"})
	}
	var fails []struct {
		Text      string
		CreatedAt string `json:"created_at"`
	}
	if err := ss.read(ctx, act, "feed_list", map[string]any{"kind": "failure", "lang": in.Lang}, &fails); err != nil {
		return nil, err
	}
	if n := len(fails); n > 0 {
		title := tr("One failure")
		if n > 1 {
			title = tr("Failures: %d", n)
		}
		out = append(out, todayLine{ID: "failures", Kind: "failures", Title: title, Text: fails[0].Text, Icon: "alert-circle", Tone: "error", Since: fails[0].CreatedAt})
	}
	var running []struct {
		ID                 int64
		Title, Command     string
		Topic, Since, Word string
	}
	if err := ss.read(ctx, act, "processes_list", map[string]any{"open": true, "lang": in.Lang}, &running); err != nil {
		return nil, err
	}
	for i, p := range running {
		if i == 2 {
			break
		}
		out = append(out, todayLine{ID: fmt.Sprint("running-", p.ID), Kind: "running", Title: cmp.Or(p.Topic, p.Title, p.Command), Text: p.Word,
			Icon: "activity", Since: p.Since})
	}
	if len(out) == 0 {
		out = append(out, todayLine{ID: "calm", Kind: "calm", Title: tr("All is well: nothing to decide, no failures."), Icon: "check", Tone: "ok"})
	}
	var runs []struct {
		State      string
		FinishedAt string `json:"finished_at"`
	}
	if err := ss.read(ctx, act, "processes_list", map[string]any{"limit": 500}, &runs); err != nil {
		return nil, err
	}
	y, m, d := ss.Now().Local().Date()
	midnight, done := store.Time(time.Date(y, m, d, 0, 0, 0, 0, time.Local)), 0
	for _, p := range runs {
		if p.State == "done" && p.FinishedAt >= midnight {
			done++
		}
	}
	out = append(out, todayLine{ID: "day", Kind: "day", Title: tr("Done today: %d", done), Icon: "checks"})
	var spent []struct {
		CostUSD float64 `json:"cost_usd"`
	}
	if err := ss.read(ctx, act, "spend_daily", map[string]any{"days": 1}, &spent); err == nil {
		var sum float64
		for _, d := range spent {
			sum += d.CostUSD
		}
		out = append(out, todayLine{ID: "spent", Kind: "spent", Title: tr("Spent today: $%.2f", sum), Icon: "coin"})
	}
	var ts []struct {
		Title        string
		ProjectTitle string `json:"project_title"`
		NextAt       string `json:"next_at"`
		Paused       bool
	}
	if err := ss.read(ctx, act, "triggers_list", map[string]any{"lang": in.Lang}, &ts); err != nil {
		return nil, err
	}
	next := -1
	for i, t := range ts {
		if !t.Paused && t.NextAt != "" && (next < 0 || t.NextAt < ts[next].NextAt) {
			next = i
		}
	}
	if next >= 0 {
		out = append(out, todayLine{ID: "next", Kind: "next", Title: tr("Next: %s", ts[next].Title), Text: ts[next].ProjectTitle, Icon: "clock",
			Since: ts[next].NextAt})
	}
	return out, nil
}

// read calls a read of deck as the caller and takes its result.
func (ss *Service) read(ctx context.Context, act registry.Actor, name string, input map[string]any, out any) error {
	raw, _ := json.Marshal(input)
	got, err := ss.Registry.Invoke(ctx, act, registry.Builtin+"."+name, raw)
	if err != nil {
		return err
	}
	b, err := json.Marshal(got)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
