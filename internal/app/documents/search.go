package documents

import (
	"context"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"deck2/internal/access"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// searchKinds are what memory_search finds: memory documents, the summaries of the master's topics, skills and
// finished processes.
var searchKinds = []string{"memory", "summary", "skill", "process"}

type memorySearchIn struct {
	Query   string   `desc:"Words to find; a hit has any of them, the more the higher, in any form: a word is found as a part of a word without its ending; words under three letters find nothing"`
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
	for _, k := range in.Kinds {
		if !slices.Contains(searchKinds, k) {
			return nil, registry.Invalid("unknown kind %q; one of %s", k, strings.Join(searchKinds, ", "))
		}
	}
	match := matchOf(in.Query)
	if match == "" {
		return []memoryHit{}, nil
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

// matchOf is the words of a query as an FTS5 query of the trigram index: each a part of a word to find, its Russian
// ending cut, quoted, any of them; words under three letters find nothing there and drop.
func matchOf(query string) string {
	var parts []string
	for _, w := range strings.FieldsFunc(query, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		if w = stem(w); utf8.RuneCountInString(w) >= 3 {
			parts = append(parts, `"`+w+`"`)
		}
	}
	return strings.Join(parts, " OR ")
}

// endings are the Russian endings stem cuts, longest first.
var endings = strings.Fields("иями ями ами ией ием иях ого его ому ему ыми ими ях ах ов ев ей ой ий ый ая яя ое ее ые ие ую юю ом ем ам ям ью а я о е ы и у ю ь й")

// stem is a word without its Russian ending when at least three letters stay.
func stem(w string) string {
	lw := strings.ToLower(w)
	for _, e := range endings {
		if cut, ok := strings.CutSuffix(lw, e); ok && utf8.RuneCountInString(cut) >= 3 {
			return cut
		}
	}
	return w
}
