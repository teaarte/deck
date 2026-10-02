package screen

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"deck2/internal/manifest"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/schemas"
)

const (
	Version   = "v0.9"
	CatalogID = "https://deck2.local/catalog/v1"
)

// Sources reads what a screen shows besides collections: the commands its blocks read, boards, commands of
// forms and the HTML of custom blocks.
type Sources interface {
	// Read calls a command that only reads, as the one who looks at the screen, decoded with integers as int64;
	// the scope adds the project and the values picked in filters that the command takes.
	Read(ctx context.Context, command string, input map[string]any, scope Scope) (any, error)
	Board(ctx context.Context, project db.Project, m *manifest.Manifest) (any, error)
	// Command is what a form needs of a command: name, title, project, input and result schemas.
	Command(ctx context.Context, workspace int64, name string) (map[string]any, error)
	Document(ctx context.Context, project db.Project, name string) (string, error)
	// DocumentView is what a document block shows: the document id, or the one named, of the record when
	// record is set; a document not written yet comes back empty.
	DocumentView(ctx context.Context, project db.Project, record, id int64, name string) (map[string]any, error)
	// Chat is what a chat block shows: the topic picked, "new" for none, empty for the newest.
	Chat(ctx context.Context, workspace int64, topic string) (any, error)
}

// Scope is whose items a command gives: a project, or with none every project of the workspace.
type Scope struct {
	Workspace int64
	Project   string
	// Filter holds the values the owner picked, for a command that narrows by them.
	Filter map[string]string
}

// Request is a screen to resolve. Project is zero for a screen deck ships.
type Request struct {
	Project  db.Project
	Manifest *manifest.Manifest
	Screen   *manifest.Screen
	Record   int64
	// Topic, when set, resolves only the blocks it changes and returns their data.
	Topic string
	// Filter holds the values the owner picked, by block id and field.
	Filter map[string]map[string]string
	// Owner is set when the owner asks; only the owner reads the chat.
	Owner bool
	// Card are the buttons of a card of a board.
	Card []manifest.Action
}

// Topics are the event topics after which a client reads blocks again; any other topic means the whole screen.
var Topics = func() []string {
	var c struct{ Topics []string }
	if err := json.Unmarshal(schemas.Catalog, &c); err != nil || len(c.Topics) == 0 {
		panic("catalog topics: " + fmt.Sprint(err))
	}
	return c.Topics
}()

// Render resolves a stored screen into A2UI messages: createSurface, updateComponents, updateDataModel.
// Clients only draw components and send actions back.
func Render(ctx context.Context, st *store.Store, src Sources, req Request) ([]map[string]any, error) {
	if !slices.Contains(Topics, req.Topic) {
		req.Topic = ""
	}
	if req.Topic != "" && changesWhen(req.Screen.Blocks, req.Screen.Record, req.Project.ID == 0, req.Topic) {
		req.Topic = ""
	}
	r := &renderer{ctx: ctx, st: st, src: src, req: req, data: map[string]any{}, read: map[string]readOf{}}
	r.scope = Scope{Workspace: req.Project.WorkspaceID, Project: req.Project.Name}
	if req.Screen.Record != "" {
		if err := r.loadRecord(); err != nil {
			return nil, err
		}
	}
	root := map[string]any{"id": "root", "component": "screen", "title": req.Screen.Title}
	r.comps = []map[string]any{root}
	root["children"] = r.blocks("b", req.Screen.Blocks)
	if r.recordTitle != "" {
		root["title"] = req.Screen.Title + ": " + r.recordTitle
	}
	surface := r.surface()
	if req.Topic != "" {
		out := []map[string]any{}
		for _, id := range slices.Sorted(maps.Keys(r.data)) {
			out = append(out, map[string]any{"version": Version,
				"updateDataModel": map[string]any{"surfaceId": surface, "path": "/" + id, "value": r.data[id]}})
		}
		return out, nil
	}
	return []map[string]any{
		{"version": Version, "createSurface": map[string]any{"surfaceId": surface, "catalogId": CatalogID}},
		{"version": Version, "updateComponents": map[string]any{"surfaceId": surface, "components": r.comps}},
		{"version": Version, "updateDataModel": map[string]any{"surfaceId": surface, "path": "/", "value": r.data}},
	}, nil
}

type renderer struct {
	ctx         context.Context
	st          *store.Store
	src         Sources
	req         Request
	scope       Scope
	comps       []map[string]any
	data        map[string]any
	record      map[string]any
	recordTitle string
	// types are the input types of the commands buttons call, read once per screen.
	types map[string]map[string]string
	read  map[string]readOf
}

// changesWhen reports whether an event of the topic may put a block on the screen or take it off: its when
// reads its source or the record of the page. The screen is then resolved whole.
func changesWhen(bs []manifest.Block, record string, deck bool, topic string) bool {
	page := "collection"
	switch {
	case deck:
		page = "processes"
	case record == manifest.DocumentRecord:
		page = "documents"
	}
	for _, b := range bs {
		switch {
		case b.When != "" && record != "" && topic == page,
			b.When != "" && b.Source != nil && topic == topicOf(b, deck),
			changesWhen(b.Blocks, record, deck, topic):
			return true
		}
		for _, t := range b.Tabs {
			if changesWhen(t.Blocks, record, deck, topic) {
				return true
			}
		}
	}
	return false
}

func (r *renderer) surface() string {
	if r.req.Project.ID == 0 {
		return "deck-" + r.req.Screen.Name
	}
	return fmt.Sprintf("p%d-%s", r.req.Project.ID, r.req.Screen.Name)
}

func (r *renderer) deck() bool { return r.req.Project.ID == 0 }

func (r *renderer) loadRecord() error {
	if r.req.Record <= 0 {
		return fmt.Errorf("screen %q shows one record; open it with the record id", r.req.Screen.Name)
	}
	if r.deck() {
		got, err := r.src.Read(r.ctx, "deck.process_get", map[string]any{"process": r.req.Record}, Scope{Workspace: r.scope.Workspace})
		rec, _ := got.(map[string]any)
		if err != nil || rec == nil {
			return fmt.Errorf("no process %d", r.req.Record)
		}
		rec["_id"] = r.req.Record
		r.record, r.recordTitle = rec, fmt.Sprintf("#%d %v", r.req.Record, rec["command"])
		return nil
	}
	if r.req.Screen.Record == manifest.DocumentRecord {
		got, err := r.src.Read(r.ctx, "deck.document_get", map[string]any{"document": r.req.Record}, r.scope)
		rec, _ := got.(map[string]any)
		if err != nil || rec == nil || rec["project"] != r.req.Project.Name {
			return fmt.Errorf("no document %d in %s", r.req.Record, r.req.Project.Name)
		}
		rec["_id"] = r.req.Record
		r.record, r.recordTitle = rec, fmt.Sprint(rec["name"])
		return nil
	}
	rec, err := r.st.Q.GetRecord(r.ctx, r.req.Record)
	if err != nil || rec.ProjectID != r.req.Project.ID || rec.Collection != r.req.Screen.Record {
		return fmt.Errorf("no %s record %d", r.req.Screen.Record, r.req.Record)
	}
	env, err := manifest.DecodeObject([]byte(rec.Data))
	if err != nil {
		return err
	}
	env["_id"], env["_updated"] = rec.ID, rec.UpdatedAt
	r.record = env
	if c := r.req.Manifest.Collection(rec.Collection); c != nil {
		r.recordTitle = c.RecordTitle(env)
	}
	return nil
}
