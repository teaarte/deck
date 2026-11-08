package processes

import (
	"context"
	"strconv"
	"strings"
	"time"

	"deck2/internal/access"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

type episodesListIn struct {
	Since   string `type:"datetime" desc:"From this time, RFC 3339"`
	Until   string `type:"datetime" opt:"" desc:"Before this time, RFC 3339; all so far without one"`
	Project string `type:"project" opt:"" desc:"Only the processes and incidents of this project; the chat stays whole"`
	Limit   int64  `opt:"" desc:"At most this many of each part, 200 without one, up to 1000"`
}

type episodeProcessView struct {
	ID         int64    `json:"id"`
	Project    string   `json:"project"`
	Command    string   `json:"command"`
	State      string   `json:"state"`
	ErrorType  string   `json:"error_type,omitempty"`
	Error      string   `json:"error,omitempty"`
	Provider   string   `json:"provider,omitempty"`
	Models     []string `json:"models,omitempty"`
	DurationMs int64    `json:"duration_ms"`
	StartedAt  string   `json:"started_at"`
	FinishedAt string   `json:"finished_at"`
	Topic      int64    `json:"topic,omitempty"`
}

type episodeIncidentView struct {
	Process int64  `json:"process"`
	Kind    string `json:"kind"`
	Project string `json:"project"`
	At      string `json:"at"`
}

type episodeMessageView struct {
	Topic      int64  `json:"topic"`
	TopicTitle string `json:"topic_title"`
	Text       string `json:"text"`
	At         string `json:"at"`
}

type episodeSummaryView struct {
	Topic      int64  `json:"topic"`
	TopicTitle string `json:"topic_title"`
	Summary    string `json:"summary"`
	At         string `json:"at"`
}

// episodesView is what happened in a period; More names the parts cut at the limit.
type episodesView struct {
	Processes []episodeProcessView  `json:"processes"`
	Incidents []episodeIncidentView `json:"incidents"`
	Messages  []episodeMessageView  `json:"messages"`
	Summaries []episodeSummaryView  `json:"summaries"`
	More      []string              `json:"more,omitempty" enum:"processes|incidents|messages|summaries"`
}

func (s *Service) episodesList(ctx context.Context, act registry.Actor, in episodesListIn) (episodesView, error) {
	out := episodesView{Processes: []episodeProcessView{}, Incidents: []episodeIncidentView{}, Messages: []episodeMessageView{},
		Summaries: []episodeSummaryView{}}
	since, err := time.Parse(time.RFC3339, in.Since)
	if err != nil {
		return out, registry.Invalid("since is not a time of RFC 3339: %s", in.Since)
	}
	// Without until the period takes all so far, what came in the same millisecond too.
	until := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	if in.Until != "" {
		if until, err = time.Parse(time.RFC3339, in.Until); err != nil {
			return out, registry.Invalid("until is not a time of RFC 3339: %s", in.Until)
		}
	}
	pid, err := access.Filter(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return out, err
	}
	master, err := access.Master(ctx, s.St.Q, act.Workspace)
	if err != nil {
		return out, err
	}
	limit := registry.Limit(in.Limit, 200, 1000)
	from, to, q := store.Time(since), store.Time(until), s.St.Q
	more := func(name string, n int) bool {
		if n > int(limit) {
			out.More = append(out.More, name)
		}
		return n > int(limit)
	}

	ps, err := q.ListEpisodeProcesses(ctx, db.ListEpisodeProcessesParams{WorkspaceID: act.Workspace, ProjectID: pid, Since: from, Until: to, Lim: limit + 1})
	if err != nil {
		return out, err
	}
	if more("processes", len(ps)) {
		ps = ps[:limit]
	}
	for _, p := range ps {
		v := episodeProcessView{ID: p.ID, Project: p.Project, Command: p.Command, State: p.State, ErrorType: p.ErrorType,
			Error: untrusted.Cut(p.Error, 300), Provider: p.Provider, StartedAt: p.StartedAt, FinishedAt: p.FinishedAt, Topic: p.Topic}
		if p.Models != "" {
			v.Models = strings.Split(p.Models, ",")
		}
		start, err1 := time.Parse(store.TimeLayout, p.StartedAt)
		end, err2 := time.Parse(store.TimeLayout, p.FinishedAt)
		if err1 == nil && err2 == nil {
			v.DurationMs = end.Sub(start).Milliseconds()
		}
		out.Processes = append(out.Processes, v)
	}

	is, err := q.ListEpisodeIncidents(ctx, db.ListEpisodeIncidentsParams{WorkspaceID: act.Workspace, ProjectID: pid, Since: from, Until: to, Lim: limit + 1})
	if err != nil {
		return out, err
	}
	if more("incidents", len(is)) {
		is = is[:limit]
	}
	for _, i := range is {
		id, kind, _ := strings.Cut(strings.TrimPrefix(i.Name, incidentPrefix), ".")
		n, _ := strconv.ParseInt(id, 10, 64)
		out.Incidents = append(out.Incidents, episodeIncidentView{Process: n, Kind: kind, Project: i.Project, At: i.Value})
	}

	ms, err := q.ListOwnerMessagesIn(ctx, db.ListOwnerMessagesInParams{WorkspaceID: act.Workspace, Since: from, Until: to, Lim: limit + 1})
	if err != nil {
		return out, err
	}
	if more("messages", len(ms)) {
		ms = ms[:limit]
	}
	for _, m := range ms {
		out.Messages = append(out.Messages, episodeMessageView{Topic: m.TopicID, TopicTitle: m.Title, Text: untrusted.Cut(m.Text, 300), At: m.CreatedAt})
	}

	ss, err := q.ListSummariesChangedIn(ctx, db.ListSummariesChangedInParams{WorkspaceID: act.Workspace, MasterID: store.ID(master.ID), Since: from, Until: to, Lim: limit + 1})
	if err != nil {
		return out, err
	}
	if more("summaries", len(ss)) {
		ss = ss[:limit]
	}
	for _, m := range ss {
		out.Summaries = append(out.Summaries, episodeSummaryView{Topic: m.ID, TopicTitle: m.Title, Summary: untrusted.Cut(m.Text, 600), At: m.CreatedAt})
	}
	return out, nil
}
