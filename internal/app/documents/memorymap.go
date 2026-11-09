package documents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"deck2/internal/access"
	"deck2/internal/docs"
	"deck2/internal/home"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
	"deck2/internal/words"
)

// MapPicture is the picture of the memory map in the data folder of the master.
const MapPicture = "memory-map.svg"

type memoryMapIn struct {
	Days int `opt:"" desc:"How many days back repeated failures and runs count; 30 without it"`
}

type memoryMap struct {
	Picture  []pictureRow `json:"picture"`
	Nodes    []nodeRow    `json:"nodes"`
	Projects []memoryRow  `json:"projects"`
	Lessons  []lessonRow  `json:"lessons"`
	Repeats  []repeatRow  `json:"repeats"`
}

// pictureRow is the picture of what feeds what, a file of the master a media block shows.
type pictureRow struct {
	File  string `json:"file"`
	Title string `json:"title"`
}

// nodeRow is one part of memory as its card shows it: what it holds, how it runs, when it last changed or ran.
type nodeRow struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Text    string `json:"text"`
	Meta    string `json:"meta,omitempty"`
	Details string `json:"details,omitempty"`
	Icon    string `json:"icon"`
	Tone    string `json:"tone,omitempty"`
	At      string `json:"at,omitempty"`
	// Command is the command of the master the node is; Trigger its schedule, which the owner runs now.
	Command string `json:"command,omitempty"`
	Trigger int64  `json:"trigger,omitempty"`
}

// memoryRow is the memory of one project: facts of the owner's words, lessons and lines of its index.
type memoryRow struct {
	Project    string `json:"project"`
	Facts      int    `json:"facts"`
	Lessons    int    `json:"lessons"`
	IndexLines int    `json:"index_lines"`
	IndexLimit int    `json:"index_limit"`
	Changed    string `json:"changed,omitempty"`
	// Index is the document of its index, 0 without one; Summary and Lines say the counts in the owner's words.
	Index   int64  `json:"index"`
	Summary string `json:"summary"`
	Lines   string `json:"lines"`
}

// lessonRow is a lesson of deck's agents, which the owner opens or deletes.
type lessonRow struct {
	Document    int64  `json:"document"`
	Project     string `json:"project"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Command     string `json:"command,omitempty"`
	Source      string `json:"source"`
	Changed     string `json:"changed"`
}

// repeatRow is a command of a project that failed the same way more than once, trials and work handed on aside.
type repeatRow struct {
	Project   string `json:"project"`
	Command   string `json:"command"`
	ErrorType string `json:"error_type"`
	Failures  int64  `json:"failures"`
	Repeats   int64  `json:"repeats"`
	Last      string `json:"last"`
}

func (s *Service) memoryMap(ctx context.Context, act registry.Actor, in memoryMapIn) (memoryMap, error) {
	ws := words.In(registry.Lang(ctx))
	say := func(format string, a ...any) string { return fmt.Sprintf(manifest.Tr(ws, format), a...) }
	out := memoryMap{Picture: []pictureRow{}, Nodes: []nodeRow{}, Projects: []memoryRow{}, Lessons: []lessonRow{}, Repeats: []repeatRow{}}
	if in.Days <= 0 {
		in.Days = 30
	}
	since := store.Time(s.Now().Add(-time.Duration(in.Days) * 24 * time.Hour))
	mp, err := access.Master(ctx, s.St.Q, act.Workspace)
	if err != nil {
		return out, err
	}
	ps, err := s.St.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return out, err
	}
	var owner memoryRow
	var facts, lessons int
	for _, p := range ps {
		row, ls, err := s.projectMemory(ctx, p)
		if err != nil {
			return out, err
		}
		out.Lessons = append(out.Lessons, ls...)
		if p.ID == mp.ID {
			owner = row
			continue
		}
		facts, lessons = facts+row.Facts, lessons+row.Lessons
		out.Projects = append(out.Projects, row)
	}
	out.Projects = append([]memoryRow{owner}, out.Projects...)
	for i, p := range out.Projects {
		out.Projects[i].Summary, out.Projects[i].Lines = say("%d facts, %d lessons", p.Facts, p.Lessons), say("%d of %d index lines", p.IndexLines, docs.IndexLimit)
	}
	sums, err := s.St.Q.TopicSummaries(ctx, act.Workspace)
	if err != nil {
		return out, err
	}
	skills, err := s.St.Q.SkillAuthors(ctx, act.Workspace)
	if err != nil {
		return out, err
	}
	edited := 0
	for _, sk := range skills {
		if sk.Owned == 1 {
			edited++
		}
	}
	rs, err := s.St.Q.RepeatFailures(ctx, db.RepeatFailuresParams{WorkspaceID: act.Workspace, Since: since})
	if err != nil {
		return out, err
	}
	var repeats int64
	for _, r := range rs {
		out.Repeats = append(out.Repeats, repeatRow{Project: r.Project, Command: r.Command, ErrorType: r.ErrorType,
			Failures: r.Failures, Repeats: r.Failures - 1, Last: r.LastAt})
		repeats += r.Failures - 1
	}
	out.Nodes = append(out.Nodes,
		nodeRow{ID: "owner", Title: manifest.Tr(ws, "The owner's memory"), Icon: "user-heart", At: owner.Changed,
			Text: say("%d facts, %d lessons", owner.Facts, owner.Lessons), Meta: say("%d of %d index lines", owner.IndexLines, docs.IndexLimit)},
		nodeRow{ID: "projects", Title: manifest.Tr(ws, "Memory of modules"), Icon: "apps",
			Text: say("%d facts, %d lessons", facts, lessons), Meta: say("%d modules", len(out.Projects)-1)},
		nodeRow{ID: "summaries", Title: manifest.Tr(ws, "Summaries of topics"), Icon: "messages", At: sums.Newest,
			Text: say("%d topics summed up", sums.N)},
		nodeRow{ID: "skills", Title: manifest.Tr(ws, "Skills"), Icon: "school",
			Text: say("%d skills, %d changed by you", len(skills), edited)},
	)
	m, err := docs.Manifest(ctx, s.St.Q, mp)
	if err != nil {
		return out, err
	}
	for _, c := range []struct{ name, icon string }{{"sleep", "moon"}, {"review", "sunrise"}, {"on_error", "bug"}, {"on_incident", "first-aid-kit"}} {
		n, err := s.commandNode(ctx, act.Workspace, mp, m, c.name, since, say)
		if err != nil {
			return out, err
		}
		n.Icon = c.icon
		out.Nodes = append(out.Nodes, n)
	}
	out.Nodes = append(out.Nodes,
		nodeRow{ID: "cli", Title: manifest.Tr(ws, "Own memory of the agents"), Icon: "brain", Tone: "muted",
			Text:    manifest.Tr(ws, "Off: Claude Code, Codex and Goose keep no memory of their own; deck keeps the one"),
			Details: manifest.Tr(ws, "Claude Code: autoMemoryEnabled false, CLAUDE_CODE_DISABLE_AUTO_MEMORY=1; Codex: features.memories false; Goose: only the developer and skills extensions")},
		nodeRow{ID: "repeats", Title: manifest.Tr(ws, "Repeated failures"), Icon: "repeat",
			Text: say("%d repeats in %d commands", repeats, len(rs)), Meta: say("last %d days", in.Days), Tone: tone(repeats > 0, "warn")},
	)
	pic, err := s.picture(ctx, mp, out)
	if err != nil {
		return out, err
	}
	out.Picture = append(out.Picture, pictureRow{File: mp.Name + "/" + pic, Title: manifest.Tr(ws, "Drawn by deck from the memory as it is now")})
	return out, nil
}

// projectMemory counts the memory of a project and lists its lessons.
func (s *Service) projectMemory(ctx context.Context, p db.Project) (memoryRow, []lessonRow, error) {
	rows, err := s.St.Q.ListMemoryTexts(ctx, db.ListMemoryTextsParams{WorkspaceID: p.WorkspaceID, ProjectID: store.ID(p.ID)})
	if err != nil {
		return memoryRow{}, nil, err
	}
	row := memoryRow{Project: p.Name, IndexLimit: docs.IndexLimit}
	var ls []lessonRow
	for _, r := range rows {
		if row.Changed == "" {
			row.Changed = r.CreatedAt
		}
		if r.Name == docs.MemoryIndex {
			row.IndexLines, row.Index = docs.IndexLines(r.Text), r.ID
			continue
		}
		f, err := docs.ParseFact(r.Text)
		if err != nil || f.Type != docs.Lesson {
			row.Facts++
			continue
		}
		row.Lessons++
		ls = append(ls, lessonRow{Document: r.ID, Project: p.Name, Name: f.Name, Description: untrusted.Line(f.Description, 200),
			Command: f.Command, Source: f.Source, Changed: r.CreatedAt})
	}
	return row, ls, nil
}

// commandNode is a command of the master that keeps memory: its models, its schedule and its last run.
func (s *Service) commandNode(ctx context.Context, workspace int64, mp db.Project, m *manifest.Manifest, name, since string,
	say func(string, ...any) string) (nodeRow, error) {
	n := nodeRow{ID: name, Title: name, Command: mp.Name + "." + name}
	cmd := m.Command(name)
	if cmd == nil {
		n.Text, n.Tone = say("deck has no command %s", name), "muted"
		return n, nil
	}
	if cmd.Title != "" {
		n.Title = manifest.Tr(manifest.Words(words.In(registry.Lang(ctx)), m, registry.Lang(ctx)), cmd.Title)
	}
	if s.Fits != nil {
		if models := s.Fits(ctx, mp.ID, cmd); len(models) > 0 {
			n.Text = strings.Join(models, " → ")
		}
	}
	ts, err := s.St.Q.ListProjectTriggers(ctx, mp.ID)
	if err != nil {
		return n, err
	}
	var meta []string
	for _, t := range ts {
		if t.Command != name {
			continue
		}
		if t.Kind == "cron" {
			n.Trigger = t.ID
		}
		switch {
		case t.Kind == "cron" && (t.Paused == 1 || mp.Paused == 1):
			meta = append(meta, say("at %s, paused", t.Spec))
		case t.Kind == "cron":
			meta = append(meta, say("at %s", t.Spec))
		default:
			meta = append(meta, say("on %s", t.Kind))
		}
	}
	runs, err := s.St.Q.CountRunsSince(ctx, db.CountRunsSinceParams{ProjectID: mp.ID, Command: name, Since: since})
	if err != nil {
		return n, err
	}
	meta = append(meta, say("%d runs lately", runs))
	n.Meta = strings.Join(meta, " · ")
	last, err := s.St.Q.LastRunOf(ctx, db.LastRunOfParams{ProjectID: mp.ID, Command: name})
	if errors.Is(err, sql.ErrNoRows) {
		return n, nil
	}
	if err != nil {
		return n, err
	}
	n.At = cmpOr(last.FinishedAt, last.StartedAt)
	n.Details = untrusted.Line(cmpOr(last.Error, strings.Trim(last.Result, `"`)), 400)
	n.Tone = tone(last.State == "error", "error")
	return n, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func tone(on bool, t string) string {
	if on {
		return t
	}
	return ""
}

// picture writes the picture of what feeds what in the data folder of the master when it changed, in the language of
// the workspace; its name with the version of its text.
func (s *Service) picture(ctx context.Context, mp db.Project, m memoryMap) (string, error) {
	lang := ""
	if s.Language != nil {
		lang = s.Language(ctx, mp.WorkspaceID)
	}
	svg := []byte(mapSVG(m, words.In(lang)))
	sum := sha256.Sum256(svg)
	path := filepath.Join(s.Home.Data(mp.ID), MapPicture)
	if old, err := os.ReadFile(path); err != nil || !bytes.Equal(old, svg) {
		if err := home.WriteFile(path, svg); err != nil {
			return "", err
		}
	}
	return MapPicture + "?v=" + hex.EncodeToString(sum[:6]), nil
}

func (s *Service) mapCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("memory_map", "Memory map",
			"The parts of deck's memory and what feeds what: the memory of the owner and of each project (facts of the owner's words, lessons, lines of its index of 150, its last change) with its lessons, summaries of topics, skills, the master's sleep, review and analysis of failures (models, schedule, last run), the agents' own memory (off), the commands that failed the same way more than once lately, and a picture of it all.",
			registry.OwnerSide, s.memoryMap).On("documents", "processes", "projects", "triggers", "assignments", "models"),
	}
}
