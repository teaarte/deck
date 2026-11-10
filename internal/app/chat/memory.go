package chat

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"deck2/master"

	"deck2/internal/docs"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// Memory is what a fresh session of a process gets of the memory, as data within docs.MemoryBudget: the owner's index
// for an agent of the master, the index of its project (or of a topic's), then the lessons of its command.
func (s *Service) Memory(ctx context.Context, p db.Process, again bool) (memory, lessons string, err error) {
	proj, err := s.St.Q.GetProject(ctx, p.ProjectID)
	if err != nil {
		return "", "", err
	}
	less, err := s.lessons(ctx, proj, p.Command)
	if err != nil {
		return "", "", err
	}
	if proj.Builtin == 1 {
		topic, _ := topicOf(p)
		memory, lessons, _, err = s.masterMemory(ctx, proj, topic, less, !again)
		return memory, lessons, err
	}
	own, _, err := s.memoryIndex(ctx, proj, "Memory index of project "+proj.Name)
	if err != nil {
		return "", "", err
	}
	out := s.fit(ctx, !again, own, less)
	if out[0] != "" {
		memory = master.Text("project_memory", struct{ Project, Index string }{proj.Name, out[0]})
	}
	return memory, out[1], nil
}

// languages name the languages of the interface for an agent; auto names none.
var languages = map[string]string{"en": "English", "ru": "Russian"}

// masterMemory is the memory an agent of the master gets with the lessons of its command, and a sign that changes
// when the memory does.
func (s *Service) masterMemory(ctx context.Context, mp db.Project, topic int64, less *part, count bool) (memory, lessons, sign string, err error) {
	own, rev, err := s.memoryIndex(ctx, mp, "Memory index of the owner")
	if err != nil {
		return "", "", "", err
	}
	v := struct{ Own, Project, Language string }{Language: languages[s.Language(ctx, mp.WorkspaceID)]}
	sign = fmt.Sprint(rev) + v.Language
	var idx *part
	if cp, ok := s.topicProject(ctx, topic); ok && cp.ID != mp.ID {
		var crev int64
		idx, crev, err = s.memoryIndex(ctx, cp, "Memory index of project "+cp.Name+", which this topic is about (document_get with project "+cp.Name+")")
		if err != nil {
			return "", "", "", err
		}
		sign += fmt.Sprintf("/%d:%d", cp.ID, crev)
	}
	out := s.fit(ctx, count, own, idx, less)
	v.Own, v.Project = out[0], out[1]
	return master.Text("owner_memory", v), out[2], sign, nil
}

// topicProject is the project a topic is about, as the list of topics names it: that of the screen of its first
// message.
func (s *Service) topicProject(ctx context.Context, topic int64) (db.Project, bool) {
	if topic == 0 {
		return db.Project{}, false
	}
	t, err := s.St.Q.GetTopic(ctx, topic)
	if err != nil {
		return db.Project{}, false
	}
	ts, err := s.topics(ctx, t.WorkspaceID, false, topic, 1, "", func(str string) string { return str })
	if err != nil || len(ts) == 0 || ts[0].Project == "" {
		return db.Project{}, false
	}
	p, err := s.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: t.WorkspaceID, Name: ts[0].Project})
	return p, err == nil
}

// part is a piece of the memory a turn loads, as data: an index, or lessons with the documents they are.
type part struct {
	label, text, note string
	lessons           []string
	ids               []int64
	// left is how many lessons were left out before the budget.
	left int
}

func (p *part) size() int {
	if p == nil {
		return 0
	}
	n := len(p.text)
	for _, l := range p.lessons {
		n += len(l) + 2
	}
	return n
}

// fit gives each part as a block of data within docs.MemoryBudget, in order of importance, with what was cut said
// after it; with count the lessons that reach the turn count as read.
func (s *Service) fit(ctx context.Context, count bool, parts ...*part) []string {
	sizes := make([]int, len(parts))
	for i, p := range parts {
		sizes[i] = p.size()
	}
	shares := docs.Share(sizes...)
	out := make([]string, len(parts))
	var reached []int64
	for i, p := range parts {
		if p == nil || sizes[i] == 0 {
			continue
		}
		var notes []string
		if p.note != "" {
			notes = append(notes, p.note)
		}
		if p.lessons == nil {
			start, cut := docs.CutBytes(p.text, shares[i])
			out[i] = untrusted.Block(p.label, start)
			if cut {
				notes = append(notes, fmt.Sprintf("deck cut this index to the %d KB of memory a turn loads; the rest: memory_search or document_get name %s.", docs.MemoryBudget>>10, docs.MemoryIndex))
			}
		} else {
			var kept []string
			size, left := 0, p.left
			for j, l := range p.lessons {
				if len(kept) == 0 && len(l) > shares[i] {
					l = untrusted.Cut(l, max(shares[i]-len("…"), 0))
				} else if size+len(l) > shares[i] {
					left += len(p.lessons) - j
					break
				}
				kept = append(kept, l)
				reached = append(reached, p.ids[j])
				size += len(l) + 2
			}
			out[i] = untrusted.Block(p.label, strings.Join(kept, "\n\n"))
			if left > 0 {
				notes = append(notes, fmt.Sprintf("deck left out %d more lessons of this command within the %d KB of memory a turn loads; the rest: memory_search or document_get.", left, docs.MemoryBudget>>10))
			}
		}
		if len(notes) > 0 {
			out[i] += "\n" + strings.Join(notes, " ")
		}
	}
	if count {
		docs.Reached(ctx, s.St, s.Now(), reached...)
	}
	return out
}

// memoryIndex is the start of the memory index of a project with what was cut of it, and its revision; nil without
// an index.
func (s *Service) memoryIndex(ctx context.Context, p db.Project, label string) (*part, int64, error) {
	row, err := s.St.Q.CurrentText(ctx, db.CurrentTextParams{WorkspaceID: p.WorkspaceID, ProjectID: store.ID(p.ID), Name: docs.MemoryIndex, Kind: docs.KindMemory})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil || strings.TrimSpace(row.Text) == "" {
		return nil, row.N, err
	}
	start, cut := docs.CutIndex(row.Text)
	return &part{label: label, text: start, note: cut}, row.N, nil
}

// lessonsMax is how many lessons of a command a turn of it gets at most.
const lessonsMax = 10

// lessons are the lessons of a command in the memory of a project that have not expired, the most confirmed first,
// then those confirmed or written last; nil when there are none.
func (s *Service) lessons(ctx context.Context, proj db.Project, command string) (*part, error) {
	rows, err := s.St.Q.ListMemoryTexts(ctx, db.ListMemoryTextsParams{WorkspaceID: proj.WorkspaceID, ProjectID: store.ID(proj.ID)})
	if err != nil {
		return nil, err
	}
	type lesson struct {
		id        int64
		text, day string
		times     int
	}
	var all []lesson
	for _, r := range rows {
		f, err := docs.ParseFact(r.Text)
		if r.Name == docs.MemoryIndex || err != nil || f.Type != docs.Lesson || f.Command != command || f.Expired(s.Now()) {
			continue
		}
		all = append(all, lesson{r.ID, strings.TrimSpace(r.Text), lessonDay(f, r.CreatedAt), f.Times()})
	}
	if len(all) == 0 {
		return nil, nil
	}
	slices.SortStableFunc(all, func(a, b lesson) int { return cmp.Or(b.times-a.times, strings.Compare(b.day, a.day)) })
	p := &part{label: fmt.Sprintf("Lessons of command %s of project %s, the most confirmed first", command, proj.Name), lessons: []string{}}
	for i, l := range all {
		if i == lessonsMax {
			p.left = len(all) - i
			break
		}
		p.lessons, p.ids = append(p.lessons, l.text), append(p.ids, l.id)
	}
	return p, nil
}

// lessonDay is the day a lesson was last confirmed, or the day of its revision without a valid one.
func lessonDay(f docs.Fact, created string) string {
	if _, err := time.Parse(time.DateOnly, f.LastConfirmed); err == nil {
		return f.LastConfirmed
	}
	return created[:min(len(created), len(time.DateOnly))]
}
