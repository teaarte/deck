package documents

import (
	"context"
	"time"

	"deck2/internal/docs"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

type memoryOverviewIn struct {
	Days int `opt:"" desc:"How many days back repeated failures count; 30 without it"`
}

type memoryOverview struct {
	Projects []memoryRow `json:"projects"`
	Repeats  []repeatRow `json:"repeats"`
}

// memoryRow is the memory of one project: facts of the owner's words, lessons and lines of its index.
type memoryRow struct {
	Project    string `json:"project"`
	Facts      int    `json:"facts"`
	Lessons    int    `json:"lessons"`
	IndexLines int    `json:"index_lines"`
	IndexLimit int    `json:"index_limit"`
	Changed    string `json:"changed,omitempty"`
}

// repeatRow is a command of a project that failed the same way more than once.
type repeatRow struct {
	Project   string `json:"project"`
	Command   string `json:"command"`
	ErrorType string `json:"error_type"`
	Failures  int64  `json:"failures"`
	Repeats   int64  `json:"repeats"`
	Last      string `json:"last"`
}

func (s *Service) memoryOverview(ctx context.Context, act registry.Actor, in memoryOverviewIn) (memoryOverview, error) {
	out := memoryOverview{Projects: []memoryRow{}, Repeats: []repeatRow{}}
	ps, err := s.St.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return out, err
	}
	for _, p := range ps {
		rows, err := s.St.Q.ListMemoryTexts(ctx, db.ListMemoryTextsParams{WorkspaceID: act.Workspace, ProjectID: store.ID(p.ID)})
		if err != nil {
			return out, err
		}
		row := memoryRow{Project: p.Name, IndexLimit: docs.IndexLimit}
		for _, r := range rows {
			if row.Changed == "" {
				row.Changed = r.CreatedAt
			}
			if r.Name == docs.MemoryIndex {
				row.IndexLines = docs.IndexLines(r.Text)
				continue
			}
			if f, err := docs.ParseFact(r.Text); err == nil && f.Type == docs.Lesson {
				row.Lessons++
			} else {
				row.Facts++
			}
		}
		out.Projects = append(out.Projects, row)
	}
	if in.Days <= 0 {
		in.Days = 30
	}
	since := store.Time(s.Now().Add(-time.Duration(in.Days) * 24 * time.Hour))
	rs, err := s.St.Q.RepeatFailures(ctx, db.RepeatFailuresParams{WorkspaceID: act.Workspace, Since: since})
	for _, r := range rs {
		out.Repeats = append(out.Repeats, repeatRow{Project: r.Project, Command: r.Command, ErrorType: r.ErrorType,
			Failures: r.Failures, Repeats: r.Failures - 1, Last: r.LastAt})
	}
	return out, err
}

func (s *Service) overviewCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("memory_overview", "Memory overview",
			"Counts the memory of each project — facts of the owner's words, lessons, lines of its index of 150, its last change — and the commands that failed the same way more than once lately.",
			registry.OwnerSide, s.memoryOverview).On("documents", "processes", "projects"),
	}
}
