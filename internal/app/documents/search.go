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
	Query   string   `desc:"Words to find; a hit has any of them, the more the higher, in any form: a word is found as a part of a word without its ending, ё as е; a word under three letters only as a word of its own"`
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
	match, short := matchOf(in.Query)
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
	f := finds{s: s, act: act, names: names, limit: registry.Limit(in.Limit, 20, 100), seen: map[int64]bool{}, out: []memoryHit{}}
	if match != "" {
		err = f.pages(ctx, func(skip, lim int64) ([]db.SearchMemoryRow, int64, error) {
			arg.Skip, arg.Lim = skip, lim
			rows, err := s.St.Q.SearchMemory(ctx, arg)
			return rows, int64(len(rows)), err
		})
	}
	// A word under three letters the trigram index cannot find is found as a word in the text, after the others.
	for _, w := range short {
		if err != nil {
			break
		}
		err = f.pages(ctx, func(skip, lim int64) ([]db.SearchMemoryRow, int64, error) {
			rows, err := s.St.Q.SearchMemoryWord(ctx, db.SearchMemoryWordParams{Word: w, WorkspaceID: arg.WorkspaceID,
				ProjectID: arg.ProjectID, Kinds: arg.Kinds, Own: arg.Own, Skip: skip, Lim: lim})
			var out []db.SearchMemoryRow
			for _, r := range rows {
				if snippet, ok := asWord(r.Body, w); ok {
					out = append(out, db.SearchMemoryRow{ID: r.ID, Kind: r.Kind, Snippet: snippet, ProjectID: r.ProjectID, Name: r.Name, Revision: r.Revision})
				}
			}
			return out, int64(len(rows)), err
		})
	}
	return f.out, err
}

// finds gathers the hits of a search the actor reads, each once, up to limit.
type finds struct {
	s     *Service
	act   registry.Actor
	names map[int64]string
	limit int64
	seen  map[int64]bool
	out   []memoryHit
}

// pages reads a page after page until limit hits or the rows end; a page gives its hits and how many rows it read.
func (f *finds) pages(ctx context.Context, page func(skip, lim int64) ([]db.SearchMemoryRow, int64, error)) error {
	for skip := int64(0); int64(len(f.out)) < f.limit; skip += f.limit {
		rows, n, err := page(skip, f.limit)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if int64(len(f.out)) < f.limit && !f.seen[r.ID] && f.s.mayFind(ctx, f.act, r) {
				f.seen[r.ID] = true
				f.out = append(f.out, hitOf(r, f.names[r.ProjectID]))
			}
		}
		if n < f.limit {
			return nil
		}
	}
	return nil
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
// ending cut, quoted, any of them; short are the words under three letters, in lower case, the index cannot find.
func matchOf(query string) (match string, short []string) {
	var parts []string
	for _, w := range strings.FieldsFunc(foldYo.Replace(query), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		switch w = stem(w); {
		case utf8.RuneCountInString(w) >= 3:
			parts = append(parts, `"`+w+`"`)
		case !slices.Contains(short, strings.ToLower(w)):
			short = append(short, strings.ToLower(w))
		}
	}
	return strings.Join(parts, " OR "), short
}

// foldYo writes ё as е, as the index keeps it.
var foldYo = strings.NewReplacer("ё", "е", "Ё", "Е")

// asWord finds w in text as a word of its own, not a part of a longer one, and gives the place around it.
func asWord(text, w string) (string, bool) {
	lower := []rune(strings.ToLower(text))
	runes, word := []rune(text), []rune(w)
	if len(lower) != len(runes) {
		lower = runes
	}
	inWord := func(i int) bool {
		return i >= 0 && i < len(runes) && (unicode.IsLetter(runes[i]) || unicode.IsNumber(runes[i]))
	}
	for i := 0; i+len(word) <= len(lower); i++ {
		if string(lower[i:i+len(word)]) != w || inWord(i-1) || inWord(i+len(word)) {
			continue
		}
		from, to := max(0, i-40), min(len(runes), i+len(word)+40)
		snippet := string(runes[from:i]) + "[" + string(runes[i:i+len(word)]) + "]" + string(runes[i+len(word):to])
		if from > 0 {
			snippet = "..." + snippet
		}
		if to < len(runes) {
			snippet += "..."
		}
		return snippet, true
	}
	return "", false
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
