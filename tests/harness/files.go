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

const SeedScript = PythonLib + `
st, out = deck("deck.records_upsert", {"collection": "items", "records": json.dumps({"name": "a"})})
with open(os.path.join(os.environ["DECK_RESULTS"], "photo.jpg"), "w") as f:
    f.write("record photo")
print(json.dumps({"record": out["ids"][0], "files": [{"path": "photo.jpg", "record": out["ids"][0]}]}))
`

const ReadScript = PythonLib + `
inp = json.load(sys.stdin)
doc, photo = inp["doc"], inp.get("photo", "")
checks = {
    "doc_read": open(doc).read() == "uploaded text",
    "doc_not_writable": not can_write(doc),
    "record_file_read": photo == "" or open(photo).read() == "record photo",
    "record_file_not_writable": photo == "" or not can_write(photo),
    "input_folder_not_writable": not can_write(os.path.join(os.path.dirname(doc), "new.txt")),
}
files = [{"file": int(os.path.basename(doc).split("-")[0]), "record": inp["rec"]}] if inp.get("rec") else []
print(json.dumps({"checks": checks, "files": files}))
`
