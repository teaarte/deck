package docs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Deck is the author of the revisions deck writes itself.
const Deck = "deck"

// File is a document deck ships: its name, kind and text.
type File struct{ Name, Kind, Text string }

// Files reads the files of fsys as documents named by their path in it, with prefix before.
func Files(fsys fs.FS, prefix string, kind func(name string) string) ([]File, error) {
	var out []File
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		name := prefix + p
		out = append(out, File{Name: name, Kind: kind(name), Text: string(b)})
		return err
	})
	return out, err
}

// Shipped is what Ship did: the documents it wrote and a note for the master of each edit it could not carry whole.
type Shipped struct {
	Changed []string
	Notes   []string
}

// Ship lays the files deck ships into a scope: another's edit merges in, deck's text over an agent's in a conflict and
// the owner's over deck's, with a note; one deck wrote last under prefix and ships no more goes.
func Ship(ctx context.Context, q *db.Queries, s Scope, prefix string, files []File, now time.Time) (Shipped, error) {
	var out Shipped
	shipped := map[string]bool{}
	for _, f := range files {
		shipped[f.Name] = true
		changed, note, err := ship(ctx, q, Ref{Scope: s, Name: f.Name}, f)
		if err != nil {
			return out, fmt.Errorf("ship %s: %w", f.Name, err)
		}
		if changed {
			out.Changed = append(out.Changed, f.Name)
		}
		if note != "" {
			out.Notes = append(out.Notes, note)
		}
	}
	ds, err := q.ListDocuments(ctx, db.ListDocumentsParams{WorkspaceID: s.Workspace, ProjectID: store.ID(s.Project)})
	if err != nil {
		return out, err
	}
	for _, d := range ds {
		if shipped[d.Name] || d.RecordID.Valid || !strings.HasPrefix(d.Name, prefix) {
			continue
		}
		r, err := Revision(ctx, q, d, 0)
		if err != nil {
			return out, err
		}
		if r.Author != Deck {
			continue
		}
		if _, err := q.DeleteDocument(ctx, db.DeleteDocumentParams{DeletedAt: sql.NullString{String: store.Time(now), Valid: true}, ID: d.ID}); err != nil {
			return out, err
		}
		out.Changed = append(out.Changed, d.Name)
	}
	return out, nil
}

func ship(ctx context.Context, q *db.Queries, ref Ref, f File) (changed bool, note string, err error) {
	d, err := q.FindDocument(ctx, ref.Find())
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, _, changed, err = Save(ctx, q, ref, f.Kind, f.Text, Deck, "shipped by deck")
		return changed, "", err
	case err != nil || d.DeletedAt.Valid:
		return false, "", err
	}
	cur, err := Revision(ctx, q, d, 0)
	if err != nil || cur.Text == f.Text {
		return false, "", err
	}
	if cur.Author == Deck {
		_, _, changed, err = Save(ctx, q, ref, f.Kind, f.Text, Deck, "shipped by deck")
		return changed, "", err
	}
	base, ok, err := lastOfDeck(ctx, q, d)
	if err != nil || !ok || base == f.Text {
		return false, "", err
	}
	owner := cur.Author == registry.Owner
	merged, conflicts := carry(f.Kind, cur.Text, base, f.Text, owner)
	if merged != "" {
		if bad := checkText(f.Kind, ref.Name, merged); bad != nil {
			merged, conflicts = "", []string{fmt.Sprintf("the merged text does not hold: %v", bad)}
		}
	}
	reason := fmt.Sprintf("shipped by deck; the edit of revision %d follows", cur.N)
	if _, _, _, err := Save(ctx, q, ref, f.Kind, f.Text, Deck, reason); err != nil {
		return false, "", err
	}
	switch {
	case merged == "" && owner && checkText(f.Kind, ref.Name, cur.Text) == nil:
		// The owner's text stays current over deck's, which is the base of the next merge.
		if _, _, _, err := Save(ctx, q, ref, f.Kind, cur.Text, cur.Author, "the owner's edit stays: what deck shipped does not merge with it"); err != nil {
			return false, "", err
		}
		conflicts = append(conflicts, "deck's new text:\n"+prefixed("  ", strings.Split(f.Text, "\n")))
	case merged == "":
		conflicts = append(conflicts, "the whole edit:\n"+prefixed("+ ", strings.Split(cur.Text, "\n")))
	case merged != f.Text:
		if _, _, _, err := Save(ctx, q, ref, f.Kind, merged, cur.Author, fmt.Sprintf("the edit of revision %d carried over to what deck shipped", cur.N)); err != nil {
			return false, "", err
		}
	}
	switch {
	case len(conflicts) > 0 && owner:
		note = fmt.Sprintf("deck shipped a new %s, but in %d places the owner's edit of revision %d changed the same part and their text "+
			"stands; tell the owner what of deck's new text they miss:\n\n%s", ref.Name, len(conflicts), cur.N, strings.Join(conflicts, "\n\n"))
	case len(conflicts) > 0:
		note = fmt.Sprintf("deck shipped a new %s, but in %d places it changed the same part as the edit of revision %d by %s and its "+
			"text stands; bring back what of the edit still holds:\n\n%s", ref.Name, len(conflicts), cur.N, cur.Author, strings.Join(conflicts, "\n\n"))
	}
	return true, note, nil
}

// carry puts the change from base to edit into next: a manifest by its parts, any other text by lines. Where both
// changed the same place next stands, or edit when it wins; each such place comes back as a diff.
func carry(kind, edit, base, next string, editWins bool) (string, []string) {
	var out []string
	if kind == KindManifest {
		a, b := edit, next
		if editWins {
			a, b = next, edit
		}
		merged, cs, err := manifest.Merge3([]byte(a), []byte(base), []byte(b))
		if err != nil {
			return "", []string{fmt.Sprintf("the edit does not merge: %v", err)}
		}
		for _, c := range cs {
			e, n := c.Edit, c.Next
			if editWins {
				e, n = n, e
			}
			out = append(out, fmt.Sprintf("%s: the edit made it %s, deck's new text %s", c.Path, orNone(e), orNone(n)))
		}
		return manifest.Canonical(string(merged)), out
	}
	if editWins {
		text, cs := Merge3(next, base, edit)
		for _, c := range cs {
			out = append(out, "deck's new text changed:\n"+prefixed("- ", c.Base)+prefixed("+ ", c.Edit)+"the edit there:\n"+prefixed("  ", c.Next))
		}
		return text, out
	}
	text, cs := Merge3(edit, base, next)
	for _, c := range cs {
		out = append(out, "the edit changed:\n"+prefixed("- ", c.Base)+prefixed("+ ", c.Edit)+"deck's new text there:\n"+prefixed("  ", c.Next))
	}
	return text, out
}

func orNone(v []byte) string {
	if v == nil {
		return "none"
	}
	return string(v)
}

// checkText is why a text cannot be a document of its kind; a manifest has to read.
func checkText(kind, name, text string) error {
	if kind == KindManifest {
		_, err := manifest.Parse([]byte(text))
		return err
	}
	return checkSkillText(kind, name, text)
}

func prefixed(p string, lines []string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(p + l + "\n")
	}
	return b.String()
}

// lastOfDeck is the text of the newest revision deck itself wrote of d; ok is false when deck wrote none.
func lastOfDeck(ctx context.Context, q *db.Queries, d db.Document) (string, bool, error) {
	rs, err := q.ListRevisions(ctx, d.ID)
	if err != nil {
		return "", false, err
	}
	for _, r := range rs {
		if r.Author == Deck {
			rev, err := Revision(ctx, q, d, r.N)
			return rev.Text, true, err
		}
	}
	return "", false, nil
}
