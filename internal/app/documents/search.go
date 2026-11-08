package documents

import (
	"context"
	"slices"
	"strings"
	"unicode"

	"deck2/internal/access"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// searchKinds are what memory_search finds: memory documents, the summaries of the master's topics, skills and
// finished processes.
var searchKinds = []string{"memory", "summary", "skill", "process"}

type memorySearchIn struct {
	Query   string   `desc:"Words to find; a hit has any of them, the more the higher, a word also as the start of a longer one"`
	Project string   `type:"project" opt:"" desc:"Only this project; without it all the caller reads: an agent its own project, the skills of the workspace and those its reads give"`
	Kinds   []string `opt:"" desc:"Only these: memory, summary (of a topic of the master), skill, process (a finished one: command, error, the start of its result)"`
	Limit   int64    `opt:"" desc:"At most this many, 20 when not given, up to 100"`
}

type memoryHit struct {
	Kind    string `json:"kind"`
	Project string `json:"project,omitempty"`
	// Name is the name of a document or the command of a process.
	Name     string  `json:"name"`
	Document int64   `json:"document,omitempty"`
	Revision int64   `json:"revision,omitempty"`
	Process  int64   `json:"process,omitempty"`
	Snippet  string  `json:"snippet"`
	Rank     float64 `json:"rank"`
}

// memorySearch finds text in the index the triggers of the base keep, by the rules of document_get and process_get.
func (s *Service) memorySearch(ctx context.Context, act registry.Actor, in memorySearchIn) ([]memoryHit, error) {
	match := matchOf(in.Query)
	if match == "" {
		return nil, registry.Invalid("query has no words")
	}
	for _, k := range in.Kinds {
		if !slices.Contains(searchKinds, k) {
			return nil, registry.Invalid("unknown kind %q; one of %s", k, strings.Join(searchKinds, ", "))
		}
	}
	arg := db.SearchMemoryParams{Query: match, WorkspaceID: act.Workspace, Kinds: strings.Join(in.Kinds, ",")}
	if act.Confined() {
		arg.Own = act.ProjectID
	}
	if in.Project != "" {
		p, err := access.ByName(ctx, s.St.Q, act, in.Project)
		if err != nil {
			return nil, err
		}
		arg.ProjectID = p.ID
	}
	names, err := access.Names(ctx, s.St.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	limit := registry.Limit(in.Limit, 20, 100)
	out := []memoryHit{}
	for arg.Lim = limit; int64(len(out)) < limit; arg.Skip += arg.Lim {
		rows, err := s.St.Q.SearchMemory(ctx, arg)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if int64(len(out)) < limit && s.mayFind(ctx, act, r) {
				out = append(out, hitOf(r, names[r.ProjectID]))
			}
		}
		if int64(len(rows)) < arg.Lim {
			break
		}
	}
	return out, nil
}

// mayFind is a hit the actor reads: a process of a project it is within, a document as document_get gives it.
func (s *Service) mayFind(ctx context.Context, act registry.Actor, r db.SearchMemoryRow) bool {
	if r.ProjectID == 0 || act.Within(r.ProjectID) {
		return true
	}
	if r.Kind == "process" {
		return false
	}
	p, err := s.St.Q.GetProject(ctx, r.ProjectID)
	return err == nil && access.MayRead(ctx, s.St.Q, act, p, "", r.Name) == nil
}

func hitOf(r db.SearchMemoryRow, project string) memoryHit {
	h := memoryHit{Kind: r.Kind, Project: project, Name: r.Name, Snippet: r.Snippet, Rank: r.Score}
	if r.ID < 0 {
		h.Process = -r.ID
	} else {
		h.Document, h.Revision = r.ID, r.Revision
	}
	return h
}

// matchOf is the words of a query as an FTS5 query: each quoted as a prefix, any of them, so no text of the caller
// reads as the syntax of FTS5.
func matchOf(query string) string {
	words := strings.FieldsFunc(query, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	for i, w := range words {
		words[i] = `"` + w + `"*`
	}
	return strings.Join(words, " OR ")
}
