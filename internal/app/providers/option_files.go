package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"deck2/internal/access"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// bigOption is the size past which a text value of the options of a model is kept as a file of the model.
const bigOption = 16 << 10

// fileKey names the stored file a value of the options of a model is, {"$file": id}.
const fileKey = "$file"

// fileOf is the id of the stored file a value of options names, or zero.
func fileOf(v any) int64 {
	m, ok := v.(map[string]any)
	if !ok {
		return 0
	}
	switch id := m[fileKey].(type) {
	case float64:
		return int64(id)
	case json.Number:
		n, _ := id.Int64()
		return n
	}
	return 0
}

// checkFiles refuses options that name a file which is no file of the model.
func (s *Service) checkFiles(ctx context.Context, workspace int64, model string, o map[string]any) error {
	for k, v := range o {
		id := fileOf(v)
		if id == 0 {
			continue
		}
		m, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: model})
		f, ferr := s.St.Q.GetFile(ctx, id)
		if err != nil || ferr != nil || f.OwnerKind != "model" || f.OwnerID != m.Model.ID {
			return registry.Invalid("option %s names file %d, which is no file of model %s", k, id, model)
		}
	}
	return nil
}

// keepBig keeps the long text values of the options of a model as its files and names them there by reference; a
// file its options no longer name goes. A reference may come back with the name and size the screen showed.
func (s *Service) keepBig(ctx context.Context, workspace int64, m db.Model) (string, error) {
	var o map[string]any
	if json.Unmarshal([]byte(m.Options), &o) != nil {
		return m.Options, nil
	}
	type spilled struct {
		key, tmp string
		size     int64
	}
	var big []spilled
	defer func() {
		for _, b := range big {
			_ = os.Remove(b.tmp)
		}
	}()
	for k, v := range o {
		text, ok := v.(string)
		if !ok || len(text) <= bigOption {
			continue
		}
		if err := os.MkdirAll(s.Home.Files(), 0o700); err != nil {
			return "", err
		}
		f, err := os.CreateTemp(s.Home.Files(), ".upload-*")
		if err != nil {
			return "", err
		}
		_, err = f.WriteString(text)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		big = append(big, spilled{k, f.Name(), int64(len(text))})
		if err != nil {
			return "", err
		}
	}
	ids := map[string]int64{}
	options := m.Options
	s.FilesMu.RLock()
	defer s.FilesMu.RUnlock()
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		changed := len(big) > 0
		for k, v := range o {
			if id := fileOf(v); id != 0 {
				f, err := q.GetFile(ctx, id)
				if err != nil || f.OwnerKind != "model" || f.OwnerID != m.ID {
					return registry.Invalid("option %s names file %d, which is no file of model %s", k, id, m.Name)
				}
				changed = changed || len(v.(map[string]any)) > 1
				o[k] = map[string]any{fileKey: id}
			}
		}
		if len(big) > 0 {
			p, err := access.Master(ctx, q, workspace)
			if err != nil {
				return err
			}
			for _, b := range big {
				f, err := q.CreateFile(ctx, db.CreateFileParams{ProjectID: p.ID, OwnerKind: "model", OwnerID: m.ID, Name: b.key, Type: "text/plain",
					Size: b.size, CreatedAt: store.Time(s.Now())})
				if err != nil {
					return err
				}
				ids[b.key] = f.ID
				o[b.key] = map[string]any{fileKey: f.ID}
			}
		}
		if changed {
			raw, _ := json.Marshal(o)
			options = string(raw)
			if err := q.SetModelOptions(ctx, db.SetModelOptionsParams{Options: options, ID: m.ID}); err != nil {
				return err
			}
		}
		return q.DeleteUnusedModelFiles(ctx, m.ID)
	})
	if err != nil {
		return "", err
	}
	for _, b := range big {
		if err := os.Rename(b.tmp, s.Home.File(ids[b.key])); err != nil {
			_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.DeleteFile(ctx, ids[b.key]) })
			return "", err
		}
	}
	return options, nil
}

// KeepBigOptions keeps the long values of the options of every model as files, for the models saved before.
func (s *Service) KeepBigOptions(ctx context.Context) {
	spaces, err := s.St.Q.ListWorkspaces(ctx)
	if err != nil {
		slog.Error("options of models", "err", err)
		return
	}
	for _, ws := range spaces {
		rows, err := s.St.Q.ListModels(ctx, ws)
		if err != nil {
			slog.Error("options of models", "workspace", ws, "err", err)
			continue
		}
		for _, r := range rows {
			if len(r.Model.Options) <= bigOption {
				continue
			}
			if _, err := s.keepBig(ctx, ws, r.Model); err != nil {
				slog.Error("options of a model", "model", r.Model.Name, "err", err)
			}
		}
	}
}

// withFiles are options with each value that is a file of the model read back as its text.
func (s *Service) withFiles(o map[string]any, model string) error {
	for k, v := range o {
		if id := fileOf(v); id != 0 {
			b, err := os.ReadFile(s.Home.File(id))
			if err != nil {
				return fmt.Errorf("deck: option %s of model %s is a file deck cannot read: %w", k, model, err)
			}
			o[k] = string(b)
		}
	}
	return nil
}

// shownOptions are the options of a model as its form shows them, a file by its name and size; none for an empty
// object.
func (s *Service) shownOptions(ctx context.Context, o string) string {
	if strings.TrimSpace(o) == "{}" {
		return ""
	}
	if !strings.Contains(o, fileKey) {
		return o
	}
	var m map[string]any
	if json.Unmarshal([]byte(o), &m) != nil {
		return o
	}
	for k, v := range m {
		if id := fileOf(v); id != 0 {
			if f, err := s.St.Q.GetFile(ctx, id); err == nil {
				m[k] = map[string]any{fileKey: id, "name": f.Name, "size": f.Size}
			}
		}
	}
	raw, _ := json.Marshal(m)
	return string(raw)
}
