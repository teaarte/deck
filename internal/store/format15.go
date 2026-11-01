package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/pressly/goose/v3"
)

// Widened begins the mark of a project whose agents format 15 let reach the domains of its scripts, the domains its
// value.
const Widened = "widened:"

// format15 raises the stored manifests and the manifests open asks would save to format 15: the network of a
// module is only its rights, so no_network and the requirement local_browser are gone.
var format15 = goose.NewGoMigration(56, &goose.GoFunc{RunTx: toFormat15}, nil)

func toFormat15(ctx context.Context, tx *sql.Tx) error {
	type of struct {
		current            bool
		workspace, project sql.NullInt64
	}
	manifests := map[int64]of{}
	rows, err := tx.QueryContext(ctx, `SELECT r.id, r.n = d.current, d.workspace_id, d.project_id FROM document_revision r
		JOIN "document" d ON d.id = r.document_id WHERE d.kind = 'manifest'`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var m of
		if err := rows.Scan(&id, &m.current, &m.workspace, &m.project); err != nil {
			_ = rows.Close()
			return err
		}
		manifests[id] = m
	}
	if err := rows.Close(); err != nil {
		return err
	}
	widened := map[of][]string{}
	err = renameColumn(ctx, tx, "document_revision", "text", func(id int64, v string) string {
		m, ok := manifests[id]
		if !ok {
			return v
		}
		var was struct {
			NoNetwork bool `json:"no_network"`
			Domains   struct {
				Read []string `json:"read"`
			} `json:"domains"`
		}
		if m.current && m.project.Valid && json.Unmarshal([]byte(v), &was) == nil && was.NoNetwork && len(was.Domains.Read) > 0 {
			widened[m] = was.Domains.Read
		}
		return manifest15(v)
	})
	if err != nil {
		return err
	}
	// A module that had no_network with domains for its scripts: its agents reach those now, which deck tells the owner.
	for m, domains := range widened {
		if _, err := tx.ExecContext(ctx, `INSERT INTO mark (workspace_id, project_id, name, value) VALUES (?, ?, ?, ?)
			ON CONFLICT (workspace_id, name) DO NOTHING`, m.workspace, m.project, fmt.Sprintf("%s%d", Widened, m.project.Int64), strings.Join(domains, ", ")); err != nil {
			return err
		}
	}
	return renameColumn(ctx, tx, "ask", "payload", func(_ int64, v string) string {
		var p struct {
			Call struct {
				Input struct {
					Manifest string `json:"manifest"`
				} `json:"input"`
			} `json:"call"`
		}
		if json.Unmarshal([]byte(v), &p) != nil || p.Call.Input.Manifest == "" {
			return v
		}
		raised := manifest15(p.Call.Input.Manifest)
		if raised == p.Call.Input.Manifest {
			return v
		}
		was, _ := json.Marshal(p.Call.Input.Manifest)
		now, _ := json.Marshal(raised)
		return strings.Replace(v, string(was), string(now), 1)
	})
}

// member is a key of a JSON object with its value as it is written.
type member struct {
	key string
	val json.RawMessage
}

// manifest15 is a manifest of format 14 in format 15, in the one indented form deck stores; any other text as it is.
func manifest15(text string) string {
	top, ok := members([]byte(text))
	if !ok || !slices.ContainsFunc(top, func(m member) bool { return m.key == "version" && string(m.val) == "14" }) {
		return text
	}
	top = slices.DeleteFunc(top, func(m member) bool { return m.key == "no_network" })
	for i := range top {
		switch top[i].key {
		case "version":
			top[i].val = json.RawMessage("15")
		case "commands":
			var cmds []json.RawMessage
			if json.Unmarshal(top[i].val, &cmds) != nil {
				return text
			}
			for j, c := range cmds {
				cmds[j] = withoutLocalBrowser(c)
			}
			top[i].val = array(cmds)
		}
	}
	var buf bytes.Buffer
	if json.Indent(&buf, object(top), "", "  ") != nil {
		return text
	}
	return buf.String()
}

// withoutLocalBrowser is a command without the requirement local_browser, and without requires when none is left.
func withoutLocalBrowser(c json.RawMessage) json.RawMessage {
	ms, ok := members(c)
	if !ok {
		return c
	}
	for i, m := range ms {
		var req []string
		if m.key != "requires" || json.Unmarshal(m.val, &req) != nil || !slices.Contains(req, "local_browser") {
			continue
		}
		req = slices.DeleteFunc(req, func(r string) bool { return r == "local_browser" })
		if len(req) == 0 {
			return object(slices.Delete(ms, i, i+1))
		}
		ms[i].val, _ = json.Marshal(req)
		return object(ms)
	}
	return c
}

// members are the members of a JSON object in their order.
func members(raw []byte) ([]member, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false
	}
	var out []member
	for dec.More() {
		k, err := dec.Token()
		key, ok := k.(string)
		if err != nil || !ok {
			return nil, false
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, false
		}
		out = append(out, member{key, v})
	}
	return out, true
}

func object(ms []member) json.RawMessage {
	b := []byte{'{'}
	for i, m := range ms {
		if i > 0 {
			b = append(b, ',')
		}
		k, _ := json.Marshal(m.key)
		b = append(append(append(b, k...), ':'), m.val...)
	}
	return append(b, '}')
}

func array(vs []json.RawMessage) json.RawMessage {
	b := []byte{'['}
	for i, v := range vs {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, v...)
	}
	return append(b, ']')
}
