package scenarios

import (
	"deck2/tests/harness"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func bodiesManifest(url string) string {
	return fmt.Sprintf(`{"version": 13,
  "commands": [
    {"name": "put", "title": "Put", "description": "Sends a file as it is.", "class": "internal",
     "input": {"type": "object", "properties": {"doc": {"type": "string", "format": "file"}}, "required": ["doc"]},
     "executor": {"type": "tool", "tool": "put_file"}},
    {"name": "form", "title": "Form", "description": "Sends a file in a form.", "class": "internal",
     "input": {"type": "object", "properties": {"doc": {"type": "string", "format": "file"}, "title": {"type": "string"}}, "required": ["doc"]},
     "executor": {"type": "tool", "tool": "post_form"}},
    {"name": "make", "title": "Make", "description": "Makes a clip and sends it at once.", "class": "internal",
     "result": {"type": "object"}, "executor": {"type": "script", "script": "make"}}],
  "scripts": [{"name": "make", "file": "scripts/make.py", "runtime": "python"}],
  "tools": {"http": [
    {"name": "put_file", "description": "Puts a file", "method": "PUT", "url": "%[1]s/put", "class": "internal", "body": {"file": "{doc}"},
     "input": {"type": "object", "properties": {"doc": {"type": "string", "format": "file"}}, "required": ["doc"]}},
    {"name": "post_form", "description": "Posts a form", "method": "POST", "url": "%[1]s/form", "class": "internal",
     "body": {"multipart": {"upload": "{doc}", "title": "Title: {title}"}},
     "input": {"type": "object", "properties": {"doc": {"type": "string", "format": "file"}, "title": {"type": "string"}}, "required": ["doc"]}}]}}`, url)
}

const makeScript = harness.PythonLib + `
with open(os.path.join(os.environ["DECK_RESULTS"], "clip.mp4"), "wb") as f:
    f.write(b"clip bytes")
st, out = deck("media.put_file", {"doc": "results/clip.mp4"})
os.symlink(os.path.join(os.environ["DECK_RESULTS"], "..", "stream"), os.path.join(os.environ["DECK_RESULTS"], "link"))
escape, _ = deck("media.put_file", {"doc": "results/link"})
print(json.dumps({"status": st, "escape": escape}))
`

func TestHTTPToolSendsFilesAsItsBody(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	got := map[string][]string{}
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/put":
			b, _ := io.ReadAll(r.Body)
			got["put"] = append(got["put"], r.Header.Get("Content-Type")+" "+string(b))
		case "/form":
			_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			mr := multipart.NewReader(r.Body, params["boundary"])
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}
				b, _ := io.ReadAll(p)
				got["form"] = append(got["form"], p.FormName()+"|"+p.FileName()+"|"+string(b))
			}
			if r.ContentLength <= 0 {
				got["form"] = append(got["form"], "no length")
			}
		}
		harness.JsonReply(w, `{"ok": true}`)
	})
	e := harness.New(t)
	e.Project("media", bodiesManifest(svc.URL))
	doc := fmt.Sprint(upload(e, "media", "photo.jpg", "image/jpeg", "JPEGDATA"))

	if p := e.WaitState(e.Start("media", "put", map[string]any{"doc": doc}), "done", "error"); p.State != "done" || got["put"][0] != "image/jpeg JPEGDATA" {
		t.Fatalf("a file body sends the file as it is: %+v %v", p, got)
	}
	if p := e.WaitState(e.Start("media", "form", map[string]any{"doc": doc, "title": "Sea"}), "done", "error"); p.State != "done" ||
		strings.Join(got["form"], ";") != "title||Title: Sea;upload|photo.jpg|JPEGDATA" {
		t.Fatalf("a multipart body sends file fields as files, with a length: %+v %v", p, got["form"])
	}
	tricky := fmt.Sprint(upload(e, "media", "note.txt", "text/plain", "a\r\n--deck-form-boundary\r\nb"))
	got["form"] = nil
	if p := e.WaitState(e.Start("media", "form", map[string]any{"doc": tricky, "title": "x"}), "done", "error"); p.State != "done" ||
		strings.Join(got["form"], ";") != "title||Title: x;upload|note.txt|a\r\n--deck-form-boundary\r\nb" {
		t.Fatalf("a file holding the text of a boundary reaches the form whole: %+v %q", p, got["form"])
	}
	e.SaveScript("media", "scripts/make.py", makeScript)
	if p := e.WaitState(e.Start("media", "make", nil), "done", "error"); !strings.Contains(string(p.Result), `"status":200`) ||
		!strings.Contains(string(p.Result), `"escape":422`) ||
		got["put"][1] != "video/mp4 clip bytes" {
		t.Fatalf("a file of the results folder goes to a tool while the script still runs: %+v %v", p, got["put"])
	}
	code, out := e.Call("media.put_file", map[string]any{"doc": "results/../../../deck.db"})
	if code == 200 || !strings.Contains(string(out), "results file") {
		t.Fatalf("a results file needs a process: %d %s", code, out)
	}
}

func TestACommandCallLargerThan32MBIsRefusedWhole(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	big := map[string]any{"text": strings.Repeat("x", 33<<20)}
	if code, out := e.Call("deck.projects_list", big); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("the API refuses a body over 32 MB: %d %.200s", code, out)
	}
	e.Project("big", harness.ScriptManifest("send", "", ""))
	e.SaveScript("big", "scripts/main.py", harness.PythonLib+`
st, out = deck("deck.records_upsert", {"collection": "items", "records": json.dumps({"name": "x" * (33 << 20)})})
print(json.dumps({"status": st}))
`)
	if p := e.WaitState(e.Start("big", "send", nil), "done", "error"); p.State != "done" || !strings.Contains(string(p.Result), "413") {
		t.Fatalf("the entry of scripts refuses a body over 32 MB rather than cutting it: %+v", p)
	}
}

func TestATextAnswerOverTheLimitIsNotPassedOnCut(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("x", 11<<20)))
	})
	e := harness.New(t)
	e.Project("big", fmt.Sprintf(`{"version": 13, "tools": {"http": [{"name": "page", "description": "Reads a page.", "method": "GET",
  "url": %q, "class": "internal", "input": {"type": "object", "properties": {}}}]}}`, svc.URL+"/page"))
	if code, out := e.Call("big.page", map[string]any{}); code == 200 || !strings.Contains(string(out), "cut answer") {
		t.Fatalf("an answer over 10 MB is an error, not its first 10 MB: %d %.200s", code, out)
	}
}
