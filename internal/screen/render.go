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

// Sources reads what a screen shows besides collections: the items of the other sources, boards,
// commands of forms and the HTML of custom blocks.
type Sources interface {
	Items(ctx context.Context, from string, scope Scope) ([]map[string]any, error)
	Board(ctx context.Context, project db.Project, m *manifest.Manifest) (any, error)
	// Command is what a form needs of a command: name, title, project, input and result schemas.
	Command(ctx context.Context, workspace int64, name string) (map[string]any, error)
	Document(ctx context.Context, project db.Project, name string) (string, error)
	// DocumentView is what a document block shows: the document id, or the one named, of the record when
	// record is set; a document not written yet comes back empty.
	DocumentView(ctx context.Context, project db.Project, record, id int64, name string) (map[string]any, error)
}

// Scope is whose items a source gives: a project, or with Project 0 every project of the workspace.
type Scope struct {
	Workspace int64
	Project   int64
	// Record narrows processes to one process.
	Record int64
	// Filter holds the values the owner picked, for a source that narrows by them in its query.
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
	r := &renderer{ctx: ctx, st: st, src: src, req: req, data: map[string]any{}}
	r.scope = Scope{Workspace: req.Project.WorkspaceID, Project: req.Project.ID}
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
		items, err := r.src.Items(r.ctx, "processes", Scope{Workspace: r.scope.Workspace, Record: r.req.Record})
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return fmt.Errorf("no process %d", r.req.Record)
		}
		r.record = items[0]
		r.recordTitle = fmt.Sprintf("#%d %v", r.req.Record, items[0]["command"])
		return nil
	}
	if r.req.Screen.Record == manifest.DocumentRecord {
		items, err := r.src.Items(r.ctx, "documents", Scope{Workspace: r.scope.Workspace, Project: r.scope.Project, Record: r.req.Record})
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return fmt.Errorf("no document %d in %s", r.req.Record, r.req.Project.Name)
		}
		r.record, r.recordTitle = items[0], fmt.Sprint(items[0]["name"])
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
