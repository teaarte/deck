package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

// Upload sends a file to deck as a form would and returns its id.
func (e *Env) Upload(project, name, ctype, content string) int64 {
	e.T.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="file"; filename=%q`, name)}
	h["Content-Type"] = []string{ctype}
	w, _ := mw.CreatePart(h)
	_, _ = io.WriteString(w, content)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", e.URL+"/api/files?project="+project, &buf)
	e.Sign(req)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	var files []struct{ ID int64 }
	if resp.StatusCode != 200 || json.Unmarshal(out, &files) != nil || len(files) != 1 {
		e.T.Fatalf("upload: %d %s", resp.StatusCode, out)
	}
	return files[0].ID
}

func FilesManifest() string {
	return `{"version": 13,
  "collections": [{"name": "items", "description": "An item.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}],
  "commands": [
    {"name": "seed", "title": "Seed", "description": "Makes a record with a file.", "class": "internal",
     "result": {"type": "object"}, "executor": {"type": "script", "script": "seed"}},
    {"name": "read", "title": "Read", "description": "Reads the files of its input.", "class": "internal",
     "input": {"type": "object", "properties": {"doc": {"type": "string", "format": "file"}, "photo": {"type": "string", "format": "file"},
       "rec": {"type": "integer", "format": "record"}}},
     "result": {"type": "object"}, "executor": {"type": "script", "script": "read"}}],
  "scripts": [{"name": "seed", "file": "scripts/seed.py", "runtime": "python"},
    {"name": "read", "file": "scripts/read.py", "runtime": "python"}]}`
}
