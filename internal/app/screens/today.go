package screens

import (
	"cmp"
	"context"
	"deck2/internal/events"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/words"
)

type todayGetIn struct {
	Lang string `opt:"" desc:"Language of the lines, such as ru; English without one"`
}

// todayLine is a line of the card of Today, the most pressing first: what waits, what failed, what runs, what the day
// did and cost and what runs next; a calm day says so first.
type todayLineView struct {
	ID    string `json:"id"`
	Kind  string `json:"kind" enum:"asks|failures|running|opens|calm|day|asked|spent|next"`
	Title string `json:"title"`
	Text  string `json:"text,omitempty"`
	Icon  string `json:"icon"`
	Tone  string `json:"tone,omitempty"`
	Since string `json:"since,omitempty"`
	// Actions open the page of the line: a press on the line presses its open.
	Actions []map[string]any `json:"actions,omitempty"`
}

// today are the lines of the card of Today, read by the reads of deck as the caller.
func (s *Service) today(ctx context.Context, act registry.Actor, in todayGetIn) ([]todayLineView, error) {
	ws := words.In(registry.Lang(ctx))
	tr := func(str string, a ...any) string { return fmt.Sprintf(manifest.TrText(ws, str), a...) }
	out := []todayLineView{}
	var asks []struct{ What, Allow string }
	if err := s.read(ctx, act, "asks_list", map[string]any{}, &asks); err != nil {
		return nil, err
	}
	if n := len(asks); n > 0 {
		title := tr("One decision waits")
		if n > 1 {
			title = tr("Decisions wait: %d", n)
		}
		out = append(out, todayLineView{ID: "asks", Kind: "asks", Title: title, Text: cmp.Or(asks[0].Allow, asks[0].What), Icon: "hand-finger", Tone: "warn"})
	}
	var fails []struct {
		Text      string
		CreatedAt string `json:"created_at"`
	}
	if err := s.read(ctx, act, "feed_list", map[string]any{"kind": "failure"}, &fails); err != nil {
		return nil, err
	}
	if n := len(fails); n > 0 {
		title := tr("One failure")
		if n > 1 {
			title = tr("Failures: %d", n)
		}
		out = append(out, todayLineView{ID: "failures", Kind: "failures", Title: title, Text: fails[0].Text, Icon: "alert-circle", Tone: "error", Since: fails[0].CreatedAt})
	}
	var running []struct {
		ID                 int64
		Title, Command     string
		Topic, Since, Word string
		Project            string
		Opens              *struct {
			Hosts []string
			Login string
		}
	}
	if err := s.read(ctx, act, "processes_list", map[string]any{"open": true}, &running); err != nil {
		return nil, err
	}
	for i, p := range running {
		if i == 2 {
			break
		}
		out = append(out, todayLineView{ID: fmt.Sprint("running-", p.ID), Kind: "running", Title: cmp.Or(p.Topic, p.Title, p.Command), Text: p.Word,
			Icon: "activity", Since: p.Since})
	}
	var network, login []string
	for _, p := range running {
		switch {
		case p.Opens == nil:
		case p.Opens.Login != "":
			login = append(login, cmp.Or(p.Title, p.Command))
		case len(p.Opens.Hosts) > 0:
			network = append(network, cmp.Or(p.Title, p.Command)+" · "+strings.Join(p.Opens.Hosts, ", "))
		}
	}
	if len(network)+len(login) > 0 {
		out = append(out, todayLineView{ID: "opens", Kind: "opens", Title: tr("Now with the network: %d · with a login: %d", len(network), len(login)),
			Text: strings.Join(append(network, login...), "\n"), Icon: "world"})
	}
	if len(out) == 0 {
		out = append(out, todayLineView{ID: "calm", Kind: "calm", Title: tr("All is well: nothing to decide, no failures."), Icon: "check", Tone: "ok"})
	}
	// The day is the owner's, by the clock of their machine, as the schedule and the spend count it.
	y, m, d := s.Now().Local().Date()
	done, err := s.St.Q.CountDoneSince(ctx, db.CountDoneSinceParams{Since: store.Time(time.Date(y, m, d, 0, 0, 0, 0, time.Local)), WorkspaceID: act.Workspace})
	if err != nil {
		return nil, err
	}
	out = append(out, todayLineView{ID: "day", Kind: "day", Title: tr("Done today: %d", done), Icon: "checks"})
	asked, err := s.St.Q.ListRecentRequests(ctx, db.ListRecentRequestsParams{WorkspaceID: act.Workspace, Since: store.Time(time.Date(y, m, d, 0, 0, 0, 0, time.Local))})
	if err != nil {
		return nil, err
	}
	if len(asked) > 0 {
		out = append(out, todayLineView{ID: "asked", Kind: "asked", Title: tr("Requests today: %d", len(asked)), Icon: "hand-finger"})
	}
	var spent []struct {
		CostUSD float64 `json:"cost_usd"`
	}
	if err := s.read(ctx, act, "spend_days_list", map[string]any{"days": 1}, &spent); err == nil {
		var sum float64
		for _, d := range spent {
			sum += d.CostUSD
		}
		out = append(out, todayLineView{ID: "spent", Kind: "spent", Title: tr("Spent today: $%.2f", sum), Icon: "coin"})
	}
	var ts []struct {
		Title        string
		ProjectTitle string `json:"project_title"`
		NextAt       string `json:"next_at"`
		Paused       bool
	}
	if err := s.read(ctx, act, "triggers_list", map[string]any{}, &ts); err != nil {
		return nil, err
	}
	next := -1
	for i, t := range ts {
		if !t.Paused && t.NextAt != "" && (next < 0 || t.NextAt < ts[next].NextAt) {
			next = i
		}
	}
	if next >= 0 {
		out = append(out, todayLineView{ID: "next", Kind: "next", Title: tr("Next: %s", ts[next].Title), Text: ts[next].ProjectTitle, Icon: "clock",
			Since: ts[next].NextAt})
	}
	for i := range out {
		out[i].Actions = opensOf(out[i])
	}
	return out, nil
}

// read calls a read of deck as the caller and takes its result.
func (s *Service) read(ctx context.Context, act registry.Actor, name string, input map[string]any, out any) error {
	raw, _ := json.Marshal(input)
	got, err := s.Registry.Invoke(ctx, act, registry.Deck(name), raw)
	if err != nil {
		return err
	}
	b, err := json.Marshal(got)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func (s *Service) todayCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("today_get", "Today in brief",
			"Lists the lines of the card of Today, the most pressing first: what waits for the owner, what failed, what runs, what the day cost and what runs next.",
			registry.OwnerSide, s.today).On(events.Asks, events.Feed, events.Processes, events.Health, events.Usage, events.Triggers, events.Projects, events.Secrets, events.Models, events.Assignments, events.Documents),
	}
}

// opensOf is the open of a line of Today: what waits on Today, a failure in Health, a run on its page, the day in
// the schedule, the spend in Spend.
func opensOf(l todayLineView) []map[string]any {
	open := func(screen string, params map[string]any, record any) []map[string]any {
		a := map[string]any{"verb": "open", "screen": screen}
		if params != nil {
			a["params"] = params
		}
		if record != nil {
			a["record"] = record
		}
		return []map[string]any{a}
	}
	switch l.Kind {
	case "asks":
		return open("cockpit", nil, nil)
	case "failures":
		return open("settings", map[string]any{"tab": "Health"}, nil)
	case "running":
		if id, err := strconv.ParseInt(strings.TrimPrefix(l.ID, "running-"), 10, 64); err == nil {
			return open("process", nil, id)
		}
	case "day", "next":
		return open("schedule", nil, nil)
	case "spent":
		return open("spend", nil, nil)
	case "opens":
		return open("cockpit", nil, nil)
	case "asked":
		return open("settings", map[string]any{"tab": "Rights"}, nil)
	}
	return nil
}
