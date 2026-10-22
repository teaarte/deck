package asks

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/app/access"
	"deck2/internal/ask"
	"deck2/internal/gateway"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

type asksListIn struct {
	Project string `type:"project" opt:"" desc:"Project name; every project without one"`
}

// askRow is what waits for the owner, with who asks, what and where in words.
type askRow struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	Project string `json:"project"`
	Process int64  `json:"process,omitempty"`
	Who     string `json:"who"`
	What    string `json:"what"`
	// Command is the command a call waits to make, for the details of a decision.
	Command   string          `json:"command,omitempty"`
	Where     string          `json:"where"`
	Text      string          `json:"text"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
	ExpiresAt string          `json:"expires_at"`
	// Expired is a confirmation that ran out; it stays until the owner clears it.
	Expired bool `json:"expired"`
	// Link is the address of the screen where the owner answers it.
	Link string `json:"link"`
}

// answerLink is the screen where the owner answers what waits.
const answerLink = "#/deck/cockpit"

// asksList is what waits for the owner, oldest first: open asks and confirmations that expired.
func (s *Service) asksList(ctx context.Context, act registry.Actor, in asksListIn) ([]askRow, error) {
	pid, err := access.Filter(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	open, err := s.St.Q.ListWorkspaceOpenAsks(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	expired, err := s.St.Q.ListWorkspaceExpiredConfirms(ctx, db.ListWorkspaceExpiredConfirmsParams{WorkspaceID: act.Workspace,
		Now: store.Time(s.Now())})
	if err != nil {
		return nil, err
	}
	names, err := access.Names(ctx, s.St.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	all := append(open, expired...)
	slices.SortFunc(all, func(x, y db.Ask) int { return cmp.Compare(x.ID, y.ID) })
	out := make([]askRow, 0, len(all))
	for _, q := range all {
		if pid != 0 && q.ProjectID.Int64 != pid {
			continue
		}
		out = append(out, s.askRow(ctx, q, names))
	}
	return out, nil
}

// Waiting are how many things wait for the owner, as asks_list gives them, by project, deck's own under "".
func (s *Service) Waiting(ctx context.Context, act registry.Actor) (map[string]int64, error) {
	rows, err := s.asksList(ctx, act, asksListIn{})
	out := map[string]int64{}
	for _, r := range rows {
		out[r.Project]++
	}
	return out, err
}

func (s *Service) askRow(ctx context.Context, q db.Ask, names map[int64]string) askRow {
	r := askRow{ID: q.ID, Kind: q.Kind, Project: names[q.ProjectID.Int64], Process: q.ProcessID.Int64, Text: q.Text, What: firstLine(q.Text),
		Payload: json.RawMessage(cmp.Or(q.Payload, "null")), CreatedAt: q.CreatedAt, ExpiresAt: q.ExpiresAt.String, Expired: q.State == ask.StateExpired, Link: answerLink}
	if q.ProcessID.Valid {
		r.Who = s.processName(ctx, q.ProcessID.Int64)
	}
	switch q.Kind {
	case ask.KindQuestion:
		var pl struct {
			Questions json.RawMessage `json:"questions"`
		}
		if json.Unmarshal([]byte(q.Payload), &pl) == nil && len(pl.Questions) > 0 {
			r.What = questionTitle(pl.Questions)
		}
	case ask.KindCall, ask.KindUnknown:
		var f gateway.Frozen
		_ = json.Unmarshal([]byte(q.Payload), &f)
		r.Who, r.What, r.Where, r.Command = s.callerName(ctx, f.Caller), f.Call.Command, f.Target, f.Call.Command
		if c, err := s.Registry.Lookup(ctx, q.WorkspaceID, f.Call.Command); err == nil {
			r.What = c.Title
			if f.Target == "" && c.Target != nil {
				r.Where = c.Target(f.Tool, f.Call.Input)
			}
		}
	case ask.KindGrant:
		var f gateway.Frozen
		_ = json.Unmarshal([]byte(q.Payload), &f)
		r.Who, r.What, r.Where = s.callerName(ctx, f.Caller), "Widen the rights of "+r.Project, strings.Join(f.Widens, "; ")
		r.Text = cmp.Or(f.Reason, "no reason given")
		r.Payload, _ = json.Marshal(map[string]any{"widens": f.Widens, "reason": f.Reason})
	case ask.KindConfirm:
		var pl struct {
			Tool  string         `json:"tool"`
			Input map[string]any `json:"input"`
		}
		if json.Unmarshal([]byte(q.Payload), &pl) == nil && pl.Tool != "" {
			r.What, r.Where = pl.Tool, confirmTarget(pl.Input)
		}
	case ask.KindSignIn:
		var pl map[string]string
		_ = json.Unmarshal([]byte(q.Payload), &pl)
		r.Where = cmp.Or(pl["site"], pl["tool"])
	}
	return r
}

// questionTitle heads a question the picker shows in full: the short headers of its questions, else "Question".
func questionTitle(raw json.RawMessage) string {
	var qs []struct {
		Header string `json:"header"`
	}
	_ = json.Unmarshal(raw, &qs)
	var hs []string
	for _, q := range qs {
		if q.Header != "" {
			hs = append(hs, q.Header)
		}
	}
	if len(hs) == 0 {
		return "Question"
	}
	return strings.Join(hs, " · ")
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func (s *Service) processName(ctx context.Context, id int64) string {
	p, err := s.St.Q.GetProcess(ctx, id)
	if err != nil {
		return fmt.Sprintf("process #%d", id)
	}
	return fmt.Sprintf("#%d %s", id, p.Command)
}

// callerName says who made a frozen call: a process, a token, a trigger, the master or the owner.
func (s *Service) callerName(ctx context.Context, c gateway.Caller) string {
	switch {
	case c.Token != 0 || c.Kind == registry.Token:
		return fmt.Sprintf("token #%d", c.Token)
	case c.Process != 0:
		return s.processName(ctx, c.Process)
	}
	return c.Kind
}

// confirmTarget is where a provider tool call goes: its URL, command or path, else its input.
func confirmTarget(in map[string]any) string {
	for _, k := range []string{"url", "command", "file_path", "path", "query"} {
		if v, ok := in[k].(string); ok && v != "" {
			return v
		}
	}
	if len(in) == 0 {
		return ""
	}
	b, _ := json.Marshal(in)
	return untrusted.Cut(string(b), 300)
}
