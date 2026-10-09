package devices

import (
	"context"
	"deck2/internal/app/access"
	"encoding/json"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// DeckPrefix starts the screen of a note which opens a screen of deck rather than of its project.
const DeckPrefix = "deck/"

// Button is a button a notification carries: a command of its project with params.
type Button struct {
	Label   string         `json:"label"`
	Command string         `json:"command"`
	Params  map[string]any `json:"params,omitempty"`
	// Icon, Confirm and Input are as for a button of a screen: a Tabler icon, a yes first, a param the owner types.
	Icon    string `json:"icon,omitempty"`
	Confirm bool   `json:"confirm,omitempty"`
	Input   string `json:"input,omitempty"`
}

type notifyIn struct {
	Project string `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
	Text    string `type:"text" desc:"What happened, in a sentence"`
	Screen  string `opt:"" desc:"Screen of the project the line opens, or deck/<screen> for a screen of deck such as deck/schedule"`
	Record  int64  `type:"record" opt:"" desc:"Record the screen shows"`
	Actions string `type:"json" opt:"" desc:"Buttons: a JSON list [{label, command, params, icon?, confirm?, input?}] with commands of the project; confirm asks the owner first, input names a param the owner types"`
}

// notify writes a line to the feed of the cockpit; push comes with stage 3. The caller decides whether it is worth it.
func (dv *Service) notify(ctx context.Context, act registry.Actor, in notifyIn) (any, error) {
	p, err := access.Project(ctx, dv.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Text) == "" {
		return nil, registry.Invalid("deck: notify needs text")
	}
	m, err := docs.Manifest(ctx, dv.St.Q, p)
	if err != nil {
		return nil, err
	}
	if name, ok := strings.CutPrefix(in.Screen, DeckPrefix); ok {
		s := dv.DeckScreen(name)
		switch {
		case s == nil:
			return nil, registry.Invalid("deck: deck has no screen %q", name)
		case s.Record != "" && in.Record == 0:
			return nil, registry.Invalid("deck: screen %q of deck shows a %s; give record", name, s.Record)
		}
	} else if in.Screen != "" {
		s := m.Screen(in.Screen)
		switch {
		case s == nil:
			return nil, registry.Invalid("deck: %s has no screen %q", p.Name, in.Screen)
		case s.Record != "" && in.Record == 0:
			return nil, registry.Invalid("deck: screen %q shows a record of %s; give record", in.Screen, s.Record)
		case s.Record != "":
			rec, err := dv.St.Q.GetRecord(ctx, in.Record)
			if err != nil || rec.ProjectID != p.ID || rec.Collection != s.Record {
				return nil, registry.Invalid("deck: no %s record %d", s.Record, in.Record)
			}
		}
	}
	var buttons []Button
	if strings.TrimSpace(in.Actions) != "" {
		if err := json.Unmarshal([]byte(in.Actions), &buttons); err != nil {
			return nil, registry.Invalid("deck: actions: a JSON array of {label, command, params}: %v", err)
		}
	}
	for _, b := range buttons {
		keys := map[string]string{}
		for k := range b.Params {
			keys[k] = ""
		}
		if b.Label == "" {
			return nil, registry.Invalid("deck: actions: a button needs label")
		}
		if err := m.CheckButton(manifest.Action{Label: b.Label, Command: b.Command, Params: keys}); err != nil {
			return nil, registry.Invalid("deck: actions %q: %v", b.Label, err)
		}
	}
	raw, _ := json.Marshal(buttons)
	var n db.Feed
	err = dv.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		n, err = q.AddFeed(ctx, db.AddFeedParams{CreatedAt: store.Time(dv.Now()), WorkspaceID: p.WorkspaceID, ProjectID: p.ID, Kind: FeedNote, Author: act.Author(),
			Text: in.Text, Screen: in.Screen, RecordID: store.ID(in.Record), Actions: string(raw), Params: "{}"})
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"notification": n.ID}, nil
}

// NotifyCommands are the commands of the feed lines of notify.
func (dv *Service) NotifyCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("notify", "Notify the owner",
			"Writes a line to the feed of the cockpit, with a screen of the project to open and buttons; the caller decides it is worth it.",
			registry.Everyone, dv.notify),
	}
}
