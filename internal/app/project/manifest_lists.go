package project

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// AddToList adds a value to a list of the project's manifest at the path of keys, as a new revision with the keys in
// their order.
func (pj *Service) AddToList(ctx context.Context, act registry.Actor, p db.Project, value string, path ...string) error {
	m, err := docs.Manifest(ctx, pj.St.Q, p)
	if err != nil {
		return err
	}
	raw, err := docs.CurrentText(ctx, pj.St.Q, docs.ProjectScope(p), docs.KindManifest, docs.ManifestName)
	if err != nil {
		return err
	}
	var doc map[string]any
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&doc); err != nil {
		return err
	}
	at := doc
	for _, k := range path[:len(path)-1] {
		next, _ := at[k].(map[string]any)
		if next == nil {
			next = map[string]any{}
			at[k] = next
		}
		at = next
	}
	last := path[len(path)-1]
	list, _ := at[last].([]any)
	if slices.Contains(list, any(value)) {
		return nil
	}
	at[last] = append(list, value)
	b := manifest.Canonical(string(manifest.Ordered(doc, []byte(raw))))
	_, err = pj.SaveManifest(ctx, act, p, []byte(b), "allow "+value+" always", 0, m.Revision)
	return err
}
