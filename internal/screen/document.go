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
		return withSave(v), nil
	case err != nil:
		return nil, err
	case d == nil || d["project"] != p.Name:
		return nil, fmt.Errorf("no document %d in %s", id, p.Name)
	}
	all, _ := d["revisions"].([]any)
	revs := make([]map[string]any, 0, len(all))
	editable, _ := d["editable"].(bool)
	for _, rv := range all {
		if m, ok := rv.(map[string]any); ok {
			rev := map[string]any{"revision": m["revision"], "author": m["author"], "reason": m["reason"], "created_at": m["created_at"]}
			if editable && fmt.Sprint(m["revision"]) != fmt.Sprint(d["revision"]) {
				rev["revert"] = map[string]any{"verb": "revert", "command": "deck.document_revert",
					"params": map[string]any{"document": d["id"], "revision": m["revision"], "if_revision": d["revision"]}}
			}
			revs = append(revs, rev)
		}
	}
	v := map[string]any{"id": d["id"], "project": p.Name, "name": d["name"], "kind": d["kind"], "text": cmp.Or(d["text"], any("")), "revision": d["revision"],
		"editable": d["editable"], "revisions": revs}
	if owner, _ := d["owner_kind"].(string); owner != "" {
		v[owner] = d["owner_id"]
	}
	return withSave(v), nil
}

// briefed leaves the body of a block, under key in its value, out of a read by anyone but the owner: its size stays,
// and whole names the block a read of which alone gives it whole.
func (r *renderer) briefed(id, key string) func(map[string]any, error) (map[string]any, error) {
	return func(out map[string]any, err error) (map[string]any, error) {
		v, _ := out["value"].(map[string]any)
		if err != nil || !r.req.Brief || v == nil {
			return out, err
		}
		body, _ := v[key].(string)
		delete(v, key)
		v["size"], v["whole"] = len(body), map[string]any{"command": "deck.screen", "block": id}
		return out, nil
	}
}

// withSave gives an editable document the button that saves its text as a new revision; the owner fills in the
// text, the reason and the revision the edit began from.
func withSave(v map[string]any) map[string]any {
	if editable, _ := v["editable"].(bool); !editable {
		return v
	}
	params := map[string]any{"project": v["project"], "name": v["name"], "kind": v["kind"]}
	if rec, ok := v["record"]; ok {
		params["record"] = rec
	}
	v["save"] = map[string]any{"verb": "save", "command": "deck.document_save", "params": params}
	return v
}
