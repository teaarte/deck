package screen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"deck2/internal/manifest"
	"deck2/internal/store/db"
	"deck2/schemas"
)

const (
	Version   = "v0.9"
	CatalogID = "https://deck2.local/catalog/v1"
)

// OwnerOnly is the error of a read only the owner may make, read by another; its block says so and is no failure.
var OwnerOnly = errors.New("only the owner sees this")

// Sources reads what a screen shows: records, the commands its blocks read and commands of forms; the screen itself
// touches no store.
type Sources interface {
	// Read calls a command that only reads, as the one who looks at the screen, decoded with integers as int64;
	// the scope adds the project, the language and the values picked in filters that the command takes.
	Read(ctx context.Context, command string, input map[string]any, scope Scope) (any, error)
	// Command is what a form needs of a command: name, title, project, input and result schemas.
	Command(ctx context.Context, workspace int64, name string) (map[string]any, error)
	// File is a file a media block shows: a stored file of the project by id or a path in its data folder, as
	// name, type, size and the url the client reads it at.
	File(ctx context.Context, project db.Project, ref any) (map[string]any, error)
}

// Scope is whose items a command gives: a project, or with none every project of the workspace.
type Scope struct {
	Workspace int64
	Project   string
	// Filter holds the values the owner picked, for a command that narrows by them.
	Filter map[string]string
	// By is the project whose rights read another project, for a block of its screen.
	By int64
	// Lang is the owner's language, for a command that says things in it.
	Lang string
}

// Request is a screen to resolve. Project is zero for a screen deck ships.
type Request struct {
	Project  db.Project
	Manifest *manifest.Manifest
	Screen   *manifest.Screen
	Record   int64
	// Topic, when set, resolves only the blocks it changes and returns their data; with Changed, only those
	// that read that project.
	Topic   string
	Changed string
	// Filter holds the values the owner picked, by block id and field.
	Filter map[string]map[string]string
	// Params are the parameters of the screen from its address, read as param.<name> when the screen declares them.
	Params map[string]string
	// Words translate the texts of the screen into Lang, the owner's language.
	Words map[string]string
	Lang  string
	// Brief gives the HTML of custom blocks and the text of documents by name and size, not whole.
	Brief bool
	// Dialog is the dialog open on the screen: the blocks of any other dialog are read only when it opens.
	Dialog string
	// Block, when set, resolves only the data of the block of this id and of the blocks inside it.
	Block string
	// Pages are how many times its limit a block shows, by block id; the owner asks for more a page at a time.
	Pages map[string]int
}

// Topics are the event topics after which a client reads blocks again; any other topic means the whole screen.
var Topics = func() []string {
	var c struct{ Topics []string }
	if err := json.Unmarshal(schemas.Catalog, &c); err != nil || len(c.Topics) == 0 {
		panic("catalog topics: " + fmt.Sprint(err))
	}
	return c.Topics
}()

// Drawn is a resolved screen: its A2UI messages, the blocks that failed, those that drew, and whether every block
// was read.
type Drawn struct {
	Messages []map[string]any
	Errors   []string
	Good     []string
	Every    bool
}

// Render resolves a stored screen into A2UI messages (createSurface, updateComponents, updateDataModel); clients only
// draw components and send actions back.
func Render(ctx context.Context, src Sources, req Request) (Drawn, error) {
	if !slices.Contains(Topics, req.Topic) {
		req.Topic = ""
	}
	if req.Topic != "" && changesWhen(req.Screen.Blocks, req.Screen.Record, req.Project.ID == 0, req.Project.Name, req.Changed, req.Topic) {
		req.Topic = ""
	}
	if req.Topic == "projects" && req.Project.ID != 0 && (req.Changed == "" || req.Changed == req.Project.Name) {
		req.Topic = ""
	}
	// The window of a dialog is read as its dialog.
	req.Block = strings.TrimSuffix(req.Block, "-in")
	if req.Block != "" && whenUnder(req.Screen.Blocks, "b", req.Block) {
		req.Block = ""
	}
	r, err := resolve(ctx, src, req, false)
	if err != nil {
		return Drawn{}, err
	}
	root := r.comps[0]
	if r.recordTitle != "" {
		root["title"] = r.tr(req.Screen.Title) + ": " + r.recordTitle
	}
	surface := r.surface()
	out := []map[string]any{}
	if req.Topic == "" && req.Block == "" {
		out = append(out,
			map[string]any{"version": Version, "createSurface": map[string]any{"surfaceId": surface, "catalogId": CatalogID}},
			map[string]any{"version": Version, "updateComponents": map[string]any{"surfaceId": surface, "components": r.comps}})
	}
	// Data goes only by the path of its block.
	for _, id := range slices.Sorted(maps.Keys(r.data)) {
		out = append(out, map[string]any{"version": Version,
			"updateDataModel": map[string]any{"surfaceId": surface, "path": "/" + id, "value": r.data[id]}})
	}
	return Drawn{Messages: out, Errors: r.errs, Good: r.good, Every: req.Topic == "" && req.Block == "" && !r.skipped}, nil
}

// Check resolves a screen on the data there is now and names each block that failed.
func Check(ctx context.Context, src Sources, req Request) ([]string, error) {
	req.Topic, req.Block = "", ""
	r, err := resolve(ctx, src, req, true)
	if err != nil {
		return nil, err
	}
	return r.errs, nil
}

func resolve(ctx context.Context, src Sources, req Request, every bool) (*renderer, error) {
	r := &renderer{ctx: ctx, src: src, req: req, data: map[string]any{}, read: map[string]readOf{}, fetched: map[string]fetchOf{}, spans: map[string]any{}}
	r.every = every
	r.scope = Scope{Workspace: req.Project.WorkspaceID, Project: req.Project.Name, Lang: req.Lang}
	r.params = map[string]any{}
	for name, typ := range req.Screen.Params {
		if v := req.Params[name]; v != "" {
			r.params[name] = manifest.Typed(v, typ)
		}
	}
	if req.Screen.Record != "" {
		if err := r.loadRecord(); err != nil {
			return nil, err
		}
	}
	root := map[string]any{"id": "root", "component": "screen", "title": r.tr(req.Screen.Title),
		"topics": screenTopics(req.Screen.Blocks, req.Screen.Record, r.deck())}
	if req.Screen.Icon != "" {
		root["icon"] = req.Screen.Icon
	}
	r.comps = []map[string]any{root}
	r.grid(root, r.blocks("b", req.Screen.Blocks))
	return r, nil
}

type renderer struct {
	ctx         context.Context
	src         Sources
	req         Request
	scope       Scope
	comps       []map[string]any
	data        map[string]any
	record      map[string]any
	recordTitle string
	// types are the input types of the commands buttons call, read once per screen.
	types map[string]map[string]string
	// results are the result schemas of the commands of deck the blocks read, by name.
	results map[string]json.RawMessage
	read    map[string]readOf
	// fetched are the items of each source read for this screen.
	fetched map[string]fetchOf
	// params are the parameters the screen declares, from its address in their types.
	params map[string]any
	// inRow counts the columns a block is inside; spans are those of standard blocks narrower than the screen.
	inRow int
	spans map[string]any
	// dialogs are the names of the dialogs a block is inside, the innermost last; every reads them all as open.
	dialogs []string
	every   bool
	// skipped is set once a block of a closed dialog was left unread; good names the blocks that drew.
	skipped bool
	good    []string
	// errs name the blocks that failed to resolve.
	errs []string
}

func (r *renderer) surface() string {
	if r.req.Project.ID == 0 {
		return "deck-" + r.req.Screen.Name
	}
	return fmt.Sprintf("p%d-%s", r.req.Project.ID, r.req.Screen.Name)
}

func (r *renderer) deck() bool { return r.req.Project.ID == 0 }

// withParam is an expression environment with the parameters of the screen as param.
func (r *renderer) withParam(env map[string]any) map[string]any {
	if len(r.req.Screen.Params) == 0 {
		return env
	}
	out := make(map[string]any, len(env)+1)
	maps.Copy(out, env)
	out["param"] = r.params
	return out
}

// eval is an expression over env and the parameters of the screen.
func (r *renderer) eval(e string, env map[string]any) (any, error) {
	return manifest.Eval(e, r.withParam(env))
}

func (r *renderer) loadRecord() error {
	if r.req.Record <= 0 {
		return fmt.Errorf("screen %q shows one record; open it with the record id", r.req.Screen.Name)
	}
	if r.deck() {
		got, err := r.src.Read(r.ctx, processRead, map[string]any{"process": r.req.Record}, Scope{Workspace: r.scope.Workspace, Lang: r.req.Lang})
		rec, _ := got.(map[string]any)
		if err != nil || rec == nil {
			return fmt.Errorf("no process %d", r.req.Record)
		}
		rec["_id"] = r.req.Record
		r.record, r.recordTitle = rec, fmt.Sprintf("#%d %v", r.req.Record, rec["command"])
		return nil
	}
	if r.req.Screen.Record == manifest.DocumentRecord {
		got, err := r.src.Read(r.ctx, documentRead, map[string]any{"document": r.req.Record}, r.scope)
		rec, _ := got.(map[string]any)
		if err != nil || rec == nil || rec["project"] != r.req.Project.Name {
			return fmt.Errorf("no document %d in %s", r.req.Record, r.req.Project.Name)
		}
		rec["_id"] = r.req.Record
		r.record, r.recordTitle = rec, fmt.Sprint(rec["name"])
		return nil
	}
	got, err := r.src.Read(r.ctx, recordRead, map[string]any{"record": r.req.Record}, r.scope)
	rec, _ := got.(map[string]any)
	env, _ := rec["data"].(map[string]any)
	if err != nil || env == nil || rec["project"] != r.req.Project.Name || rec["collection"] != r.req.Screen.Record {
		return fmt.Errorf("no %s record %d", r.req.Screen.Record, r.req.Record)
	}
	env["_id"], env["_updated"] = rec["id"], rec["updated_at"]
	r.record = env
	if c := r.req.Manifest.Collection(r.req.Screen.Record); c != nil {
		r.recordTitle = c.RecordTitle(env)
	}
	return nil
}
