package feed

import (
	"context"
	"encoding/json"
	"strings"

	"deck2/internal/access"
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

type ownerNotifyIn struct {
	Project string `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
	Text    string `type:"text" desc:"What happened, in a sentence"`
	Screen  string `opt:"" desc:"Screen of the project the line opens, or deck/<screen> for a screen of deck such as deck/schedule"`
	Record  int64  `type:"record" opt:"" desc:"Record the screen shows"`
	Actions string `type:"json" opt:"" desc:"Buttons: a JSON list [{label, command, params, icon?, confirm?, input?}] with commands of the project; confirm asks the owner first, input names a param the owner types"`
	Cause   string `opt:"" desc:"What the note is about, as the subject of a row of health such as model:groq/llama-70b or provider:claude-code: the unread notes of one cause of a day, of any module, are one note with each module's part"`
}

// notify writes a line to the feed of the cockpit; push comes with stage 3. The caller decides whether it is worth it.
func (s *Service) notify(ctx context.Context, act registry.Actor, in ownerNotifyIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Text) == "" {
		return nil, registry.Invalid("owner_notify needs text")
	}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return nil, err
	}
	if name, ok := strings.CutPrefix(in.Screen, DeckPrefix); ok {
		sc := manifest.DeckScreen(name)
		switch {
		case sc == nil:
			return nil, registry.Invalid("deck has no screen %q", name)
		case sc.Record != "" && in.Record == 0:
			return nil, registry.Invalid("screen %q of deck shows a %s; give record", name, sc.Record)
		}
	} else if in.Screen != "" {
		sc := m.Screen(in.Screen)
		switch {
		case sc == nil:
			return nil, registry.Invalid("%s has no screen %q", p.Name, in.Screen)
		case sc.Record != "" && in.Record == 0:
			return nil, registry.Invalid("screen %q shows a record of %s; give record", in.Screen, sc.Record)
		case sc.Record != "":
			rec, err := s.St.Q.GetRecord(ctx, in.Record)
			if err != nil || rec.ProjectID != p.ID || rec.Collection != sc.Record {
				return nil, registry.Invalid("no %s record %d", sc.Record, in.Record)
			}
		}
	}
	var buttons []Button
	if strings.TrimSpace(in.Actions) != "" {
		if err := json.Unmarshal([]byte(in.Actions), &buttons); err != nil {
			return nil, registry.Invalid("actions: a JSON array of {label, command, params}: %v", err)
		}
	}
	for _, b := range buttons {
		keys := map[string]string{}
		for k := range b.Params {
			keys[k] = ""
		}
		if b.Label == "" {
			return nil, registry.Invalid("actions: a button needs label")
		}
		if err := m.CheckButton(manifest.Action{Label: b.Label, Command: b.Command, Params: keys}); err != nil {
			return nil, registry.Invalid("actions %q: %v", b.Label, err)
		}
	}
	raw, _ := json.Marshal(buttons)
	var id int64
	at := store.Time(s.Now())
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		id, err = store.AddNote(ctx, q, db.AddFeedParams{CreatedAt: at, WorkspaceID: p.WorkspaceID, ProjectID: p.ID, Author: act.Author(),
			Text: in.Text, Screen: in.Screen, RecordID: store.ID(in.Record), Actions: string(raw), Params: "{}", Cause: strings.TrimSpace(in.Cause),
			ProcessID: store.ID(act.ProcessID)})
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"notification": id}, nil
}

func (s *Service) notifyCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("owner_notify", "Notify the owner",
			"Writes a line to the feed of the cockpit, with a screen of the project to open and buttons; the caller decides it is worth it.",
			registry.Everyone, s.notify),
	}
}
