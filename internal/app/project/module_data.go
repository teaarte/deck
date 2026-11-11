package project

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"deck2/internal/access"
	"deck2/internal/docs"
	"deck2/internal/home"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// dataDir is the folder of a .zip of a module that holds its data: records, memory and its data folder. A module
// read from it without its data leaves the folder out, as a hidden one.
const dataDir = ".deck-data"

// recordLine is a record of a module as a .zip keeps it, a line of records/<collection>.jsonl.
type recordLine struct {
	Key  string          `json:"key"`
	Data json.RawMessage `json:"data"`
}

// writeData puts the records, memory and data folder of a project into the .zip under <name>/.deck-data; the
// documents of its records and its stored files stay with deck.
func (s *Service) writeData(ctx context.Context, zw *zip.Writer, p db.Project) (int, error) {
	base := p.Name + "/" + dataDir + "/"
	n := 0
	counts, err := s.St.Q.CountRecords(ctx, p.ID)
	if err != nil {
		return n, err
	}
	for _, c := range counts {
		rs, err := s.St.Q.ListRecords(ctx, db.ListRecordsParams{ProjectID: p.ID, Collection: c.Collection})
		if err != nil {
			return n, err
		}
		w, err := zw.Create(base + "records/" + c.Collection + ".jsonl")
		if err != nil {
			return n, err
		}
		enc := json.NewEncoder(w)
		for _, r := range rs {
			if err := enc.Encode(recordLine{Key: r.Key, Data: json.RawMessage(r.Data)}); err != nil {
				return n, err
			}
		}
		n++
	}
	ds, err := s.St.Q.ListDocuments(ctx, db.ListDocumentsParams{WorkspaceID: p.WorkspaceID, ProjectID: store.ID(p.ID)})
	if err != nil {
		return n, err
	}
	for _, d := range ds {
		if d.RecordID.Valid || d.Kind != docs.KindMemory {
			continue
		}
		text, err := docs.CurrentText(ctx, s.St.Q, docs.ProjectScope(p), d.Kind, d.Name)
		if err != nil {
			return n, err
		}
		w, err := zw.Create(base + d.Name)
		if err != nil {
			return n, err
		}
		if _, err := io.WriteString(w, text); err != nil {
			return n, err
		}
		n++
	}
	root, err := os.OpenRoot(s.Home.Data(p.ID))
	if os.IsNotExist(err) {
		return n, nil
	} else if err != nil {
		return n, err
	}
	defer root.Close()
	err = fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		// A file turned into a link, a pipe or a way out of the data folder after the walk saw it is left out.
		if home.Plain(root, rel) != nil {
			return nil
		}
		f, err := home.OpenRegular(root, rel)
		if err != nil {
			return nil
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			return nil
		}
		w, err := zw.Create(base + "data/" + rel)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, io.LimitReader(f, fi.Size())); err != nil {
			return err
		}
		n++
		return nil
	})
	return n, err
}

// The most data an import of a module brings back: its unpacked bytes and its records.
const (
	maxDataBytes   = 4 << 30
	maxDataRecords = 200_000
	recordBatch    = 500
)

// restoreData brings back what a .zip with the data of a module holds, bounded: records by the schema and key of its
// collections, the memory facts deck takes, then its files, so a refusal leaves no file behind.
func (s *Service) restoreData(ctx context.Context, act registry.Actor, p db.Project, zr *zip.Reader) (int, error) {
	prefix := ""
	for _, e := range zr.File {
		if i := strings.Index("/"+path.Clean(e.Name), "/"+dataDir+"/"); i >= 0 {
			prefix = path.Clean(e.Name)[:i] + dataDir + "/"
			break
		}
	}
	if prefix == "" {
		return 0, nil
	}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return 0, err
	}
	var records []db.UpsertRecordParams
	memory := map[string]string{}
	var files []*zip.File
	var total uint64
	for _, e := range zr.File {
		name, ok := strings.CutPrefix(path.Clean(e.Name), prefix)
		if !ok || e.FileInfo().IsDir() {
			continue
		}
		if total += e.UncompressedSize64; total > maxDataBytes {
			return 0, registry.Invalid("the data of the module holds more than 4 GB unpacked")
		}
		switch {
		case strings.HasPrefix(name, "records/") && strings.HasSuffix(name, ".jsonl"):
			collection := strings.TrimSuffix(strings.TrimPrefix(name, "records/"), ".jsonl")
			c := m.Collection(collection)
			if c == nil {
				return 0, registry.Invalid("the data of the module has records of %s, a collection its manifest does not have", collection)
			}
			r, err := e.Open()
			if err != nil {
				return 0, err
			}
			sc := bufio.NewScanner(io.LimitReader(r, int64(e.UncompressedSize64)))
			sc.Buffer(make([]byte, 1<<20), 16<<20)
			for sc.Scan() {
				var l recordLine
				var data map[string]any
				if err := json.Unmarshal(sc.Bytes(), &l); err == nil {
					err = json.Unmarshal(l.Data, &data)
				}
				if data == nil {
					_ = r.Close()
					return 0, registry.Invalid("a record of %s is no JSON object", collection)
				}
				if err := c.Validate(data); err != nil {
					_ = r.Close()
					return 0, registry.Invalid("a record of %s: %v", collection, err)
				}
				key, err := c.RecordKey(data)
				if err != nil {
					_ = r.Close()
					return 0, registry.Invalid("a record of %s: %v", collection, err)
				}
				if len(records) == maxDataRecords {
					_ = r.Close()
					return 0, registry.Invalid("the data of the module holds more than %d records", maxDataRecords)
				}
				raw, _ := json.Marshal(data)
				records = append(records, db.UpsertRecordParams{ProjectID: p.ID, Collection: collection, Key: key, Data: string(raw), Now: store.Time(s.Now())})
			}
			_ = r.Close()
			if err := sc.Err(); err != nil {
				return 0, err
			}
		case strings.HasPrefix(name, docs.MemoryDir):
			r, err := e.Open()
			if err != nil {
				return 0, err
			}
			b, err := io.ReadAll(io.LimitReader(r, docs.MaxFile+1))
			_ = r.Close()
			if err != nil || len(b) > docs.MaxFile {
				continue
			}
			memory[name] = string(b)
		case strings.HasPrefix(name, "data/"):
			dst := filepath.Join(s.Home.Data(p.ID), filepath.FromSlash(strings.TrimPrefix(name, "data/")))
			if !home.Within(s.Home.Data(p.ID), dst) {
				return 0, registry.Invalid("%s leaves the data folder", e.Name)
			}
			files = append(files, e)
		}
	}
	taken := 0
	for i := 0; i < len(records); i += recordBatch {
		batch := records[i:min(i+recordBatch, len(records))]
		if err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			for _, r := range batch {
				if _, err := q.UpsertRecord(ctx, r); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return taken, err
		}
		taken += len(batch)
	}
	// A memory fact deck does not take is left out, as one too large is.
	for name, text := range memory {
		err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			_, _, _, err := docs.Save(ctx, q, docs.Ref{Scope: docs.ProjectScope(p), Name: name}, docs.KindMemory, text, act.Author(), "imported with its data")
			return err
		})
		if err == nil {
			taken++
		}
	}
	for _, e := range files {
		name, _ := strings.CutPrefix(path.Clean(e.Name), prefix)
		dst := filepath.Join(s.Home.Data(p.ID), filepath.FromSlash(strings.TrimPrefix(name, "data/")))
		r, err := e.Open()
		if err != nil {
			return taken, err
		}
		err = home.WriteFrom(dst, io.LimitReader(r, int64(e.UncompressedSize64)))
		_ = r.Close()
		if err != nil {
			return taken, err
		}
		taken++
	}
	return taken, nil
}

type projectResetIn struct {
	Project string `type:"project" desc:"Project name"`
}

// projectReset empties a module after a snapshot of it: its records, the documents of its records, its memory, its
// files and its data folder; its manifest, prompts, scripts, secrets and settings stay.
func (s *Service) projectReset(ctx context.Context, act registry.Actor, in projectResetIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	if p.Builtin == 1 {
		return nil, registry.Invalid("%s is built into deck; its data is not reset", p.Name)
	}
	// A run of the module would write back what the reset takes away.
	if open, err := s.St.Q.ListOpenProcesses(ctx, db.ListOpenProcessesParams{WorkspaceID: p.WorkspaceID, ProjectID: p.ID}); err != nil || len(open) > 0 {
		if err == nil {
			err = registry.Conflict("%s has %d runs going; stop them or wait, then reset its data", p.Name, len(open))
		}
		return nil, err
	}
	snapshot, err := s.Snapshot(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("deck: the snapshot before resetting %s failed, nothing was reset: %w", p.Name, err)
	}
	var records int64
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := q.DeleteProjectRecordDocuments(ctx, store.ID(p.ID)); err != nil {
			return err
		}
		var err error
		if records, err = q.DeleteProjectRecords(ctx, p.ID); err != nil {
			return err
		}
		if err := q.DeleteProjectRecordSnapshots(ctx, p.ID); err != nil {
			return err
		}
		if err := q.DropProjectMemory(ctx, db.DropProjectMemoryParams{DeletedAt: store.At(s.Now()), ProjectID: store.ID(p.ID)}); err != nil {
			return err
		}
		return q.DeleteProjectFiles(ctx, p.ID)
	})
	if err != nil {
		return nil, err
	}
	data := s.Home.Data(p.ID)
	if es, err := os.ReadDir(data); err == nil {
		for _, e := range es {
			_ = os.RemoveAll(filepath.Join(data, e.Name()))
		}
	}
	params, _ := json.Marshal(map[string]any{"project": p.Name, "snapshot": snapshot})
	s.Gateway.Journal(ctx, act, 0, registry.Deck("project_reset"), registry.External, "done", string(params), "")
	return map[string]any{"reset": p.Name, "records": records, "snapshot": snapshot}, nil
}

func (s *Service) dataCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("project_reset", "Reset the data of a module",
			"Empties a module after a snapshot of deck: its records, their documents, its memory, its files and its data folder; its manifest, prompts, scripts, secrets and settings stay.",
			registry.OwnerOnly, s.projectReset),
	}
}

// restoreUpload brings back the data an uploaded .zip of a module holds into the project made of it.
func (s *Service) restoreUpload(ctx context.Context, act registry.Actor, p db.Project, ref string) (int, error) {
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil {
		return 0, nil
	}
	f, err := s.St.Q.GetFile(ctx, id)
	if err != nil {
		return 0, err
	}
	file, err := os.Open(s.Home.File(f.ID))
	if err != nil {
		return 0, err
	}
	defer file.Close()
	if !withData(file, f.Size) {
		return 0, nil
	}
	zr, err := zip.NewReader(file, f.Size)
	if err != nil {
		return 0, err
	}
	return s.restoreData(ctx, act, p, zr)
}
