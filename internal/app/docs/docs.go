package docs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"deck2/internal/registry"
	"deck2/internal/store/db"
)

const (
	KindPrompt   = "prompt"
	KindManifest = "manifest"
	KindSkill    = "skill"
	KindScript   = "script"
	KindCustom   = "custom"
	KindDoc      = "doc"
	KindArtifact = "artifact"
)

var Kinds = []string{KindPrompt, KindManifest, KindScript, KindSkill, KindCustom, KindArtifact, "memory", KindDoc}

const (
	ManifestName = "manifest.json"
	PromptName   = "prompt.md"
)

var docSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Scope is where documents live: a project of a workspace, or the workspace itself when Project is 0.
type Scope struct {
	Workspace int64
	Project   int64
}

func ProjectScope(p db.Project) Scope { return Scope{Workspace: p.WorkspaceID, Project: p.ID} }

// Ref locates a document; OwnerKind binds it to a record or card.
type Ref struct {
	Scope
	OwnerKind string
	OwnerID   int64
	Name      string
}

func (r Ref) Find() db.FindDocumentParams {
	return db.FindDocumentParams{WorkspaceID: r.Workspace, ProjectID: r.Project, OwnerKind: r.OwnerKind, OwnerID: r.OwnerID, Name: r.Name}
}

func checkDocName(name string) error {
	if name == "" || len(name) > 200 {
		return registry.Invalid("deck: document name must be 1-200 characters")
	}
	for _, seg := range strings.Split(name, "/") {
		if !docSegment.MatchString(seg) {
			return registry.Invalid("deck: document name %q must be a relative path of letters, digits, '.', '_' and '-'", name)
		}
	}
	return nil
}

// Save writes a new revision and makes it current, creating the document on first save.
func Save(ctx context.Context, q *db.Queries, ref Ref, kind, text, author, reason string) (db.Document, int64, error) {
	if !slices.Contains(Kinds, kind) {
		return db.Document{}, 0, registry.Invalid("deck: unknown document kind %q; one of %s", kind, strings.Join(Kinds, ", "))
	}
	if err := checkDocName(ref.Name); err != nil {
		return db.Document{}, 0, err
	}
	if want, ok := reservedNames[ref.Name]; ok && ref.OwnerKind == "" && kind != want {
		return db.Document{}, 0, registry.Invalid("deck: %q is reserved for the %s document", ref.Name, want)
	}
	if err := checkSkillText(kind, ref.Name, text); err != nil {
		return db.Document{}, 0, err
	}
	d, err := q.FindDocument(ctx, ref.Find())
	switch {
	case errors.Is(err, sql.ErrNoRows):
		d, err = q.CreateDocument(ctx, db.CreateDocumentParams{WorkspaceID: ref.Workspace, ProjectID: ref.Project,
			OwnerKind: ref.OwnerKind, OwnerID: ref.OwnerID, Kind: kind, Name: ref.Name})
		if err != nil {
			return d, 0, err
		}
	case err != nil:
		return d, 0, err
	case d.Kind != kind:
		return d, 0, registry.Invalid("deck: document %q is a %s, not a %s", ref.Name, d.Kind, kind)
	}
	n, err := q.NextRevision(ctx, d.ID)
	if err != nil {
		return d, 0, err
	}
	if _, err := q.AddRevision(ctx, db.AddRevisionParams{DocumentID: d.ID, N: n, Text: text, Author: author, Reason: reason}); err != nil {
		return d, 0, err
	}
	if err := q.SetDocumentCurrent(ctx, db.SetDocumentCurrentParams{Current: n, ID: d.ID}); err != nil {
		return d, 0, err
	}
	d.Current = n
	return d, n, nil
}

// CurrentText returns the current text of a document of the given kind ("" for any); empty when missing.
func CurrentText(ctx context.Context, q *db.Queries, s Scope, kind, name string) (string, error) {
	row, err := q.CurrentText(ctx, db.CurrentTextParams{WorkspaceID: s.Workspace, ProjectID: s.Project, Name: name, Kind: kind})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return row.Text, err
}

// reservedNames bind document names the core reads to their kind.
var reservedNames = map[string]string{ManifestName: KindManifest, PromptName: KindPrompt}

func Get(ctx context.Context, q *db.Queries, id int64) (db.Document, error) {
	d, err := q.GetDocument(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return d, registry.NotFound("deck: no document %d", id)
	}
	return d, err
}

func Revision(ctx context.Context, q *db.Queries, d db.Document, n int64) (db.DocumentRevision, error) {
	if n == 0 {
		n = d.Current
	}
	r, err := q.GetRevision(ctx, db.GetRevisionParams{DocumentID: d.ID, N: n})
	if errors.Is(err, sql.ErrNoRows) {
		return r, registry.NotFound("deck: document %d has no revision %d", d.ID, n)
	}
	return r, err
}

func RevertReason(n int64, reason string) string {
	if reason == "" {
		return fmt.Sprintf("revert to revision %d", n)
	}
	return fmt.Sprintf("revert to revision %d: %s", n, reason)
}

// Description is the first line of a project prompt, without a markdown heading mark.
func Description(prompt string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(prompt), "\n")
	return strings.TrimSpace(strings.TrimLeft(line, "# "))
}

// SkillsDir is where skill documents live; a skill is the folder skills/<name>/ with SKILL.md in it.
const SkillsDir = "skills/"

// SkillOf is the skill a document name belongs to, or "" when it is not under skills/<name>/.
func SkillOf(name string) string {
	rest, ok := strings.CutPrefix(name, SkillsDir)
	skill, file, ok2 := strings.Cut(rest, "/")
	if !ok || !ok2 || file == "" {
		return ""
	}
	return skill
}

// checkSkillText checks that skill files sit in a skill folder and SKILL.md carries the AgentSkills frontmatter.
func checkSkillText(kind, name, text string) error {
	skill := SkillOf(name)
	if (kind == KindSkill) != (skill != "") {
		return registry.Invalid("deck: skill files are named skills/<skill>/<file>, and only skill files are")
	}
	if name != SkillsDir+skill+"/SKILL.md" {
		return nil
	}
	front, ok := strings.CutPrefix(text, "---\n")
	end := strings.Index(front, "\n---")
	if !ok || end < 0 {
		return registry.Invalid("deck: SKILL.md must start with YAML frontmatter between --- lines")
	}
	var meta map[string]any
	if err := yaml.Unmarshal([]byte(front[:end]), &meta); err != nil || meta == nil {
		return registry.Invalid("deck: SKILL.md frontmatter is not a YAML mapping")
	}
	for key := range meta {
		if k := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(key)), "_", "-"); k == "allowed-tools" || k == "hooks" {
			return registry.Invalid("deck: SKILL.md may not carry %q: tools and their rights come from the deck gateway", key)
		}
	}
	if d, _ := meta["description"].(string); strings.TrimSpace(d) == "" {
		return registry.Invalid("deck: SKILL.md frontmatter needs \"description\"")
	}
	if n, _ := meta["name"].(string); n != skill {
		return registry.Invalid("deck: SKILL.md frontmatter needs \"name: %s\", the name of its folder", skill)
	}
	return nil
}
