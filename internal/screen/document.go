package screen

import (
	"cmp"
	"fmt"
	"net/http"

	"deck2/internal/docs"
	"deck2/internal/registry"
)

// document is what a document block shows, read as the viewer: the document id, or the one named, of the record
// when record is set, with its revisions; a document not written yet comes back empty.
func (r *renderer) document(record, id int64, name string) (map[string]any, error) {
	p := r.req.Project
	in := map[string]any{"document": id, "history": true}
	if id == 0 {
		in = map[string]any{"project": p.Name, "name": name, "history": true}
		if record != 0 {
			in["record"] = record
		}
	}
	got, err := r.src.Read(r.ctx, documentRead, in, r.scope)
	d, _ := got.(map[string]any)
	switch {
	case id == 0 && registry.Status(err) == http.StatusNotFound:
		v := map[string]any{"id": 0, "project": p.Name, "name": name, "kind": docs.KindDoc, "text": "", "revision": 0,
			"editable": docs.Editable(docs.KindDoc, name), "revisions": []map[string]any{}}
		if record != 0 {
			v["record"] = record
		}
		return v, nil
	case err != nil:
		return nil, err
	case d == nil || d["project"] != p.Name:
		return nil, fmt.Errorf("no document %d in %s", id, p.Name)
	}
	all, _ := d["revisions"].([]any)
	revs := make([]map[string]any, 0, len(all))
	for _, rv := range all {
		if m, ok := rv.(map[string]any); ok {
			revs = append(revs, map[string]any{"revision": m["revision"], "author": m["author"], "reason": m["reason"], "created_at": m["created_at"]})
		}
	}
	v := map[string]any{"id": d["id"], "project": p.Name, "name": d["name"], "kind": d["kind"], "text": cmp.Or(d["text"], any("")), "revision": d["revision"],
		"editable": d["editable"], "revisions": revs}
	if owner, _ := d["owner_kind"].(string); owner != "" {
		v[owner] = d["owner_id"]
	}
	if text, _ := v["text"].(string); r.req.Brief {
		v["text"], v["size"] = "", len(text)
	}
	return v, nil
}
