package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeResponses is an OpenAI Responses API whose model follows the steps of a prompt as written: a shell command
// line runs, a skill is read and its command run, the ask tool asks, result_submit hands in; then it ends in words.
type fakeResponses struct {
	t    *testing.T
	URL  string
	mu   sync.Mutex
	logs []string
	seen map[string]bool
}

func NewResponses(t *testing.T) *fakeResponses {
	f := &fakeResponses{t: t, seen: map[string]bool{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	f.URL = srv.URL + "/v1"
	return f
}

// serveResponses is the fake model of a test of deck's image, a program in its own container.
func serveResponses(addr string) {
	f := &fakeResponses{seen: map[string]bool{}}
	fmt.Fprintln(os.Stderr, http.ListenAndServe(addr, f))
	os.Exit(1)
}

func (f *fakeResponses) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/responses") {
		http.NotFound(w, r)
		return
	}
	b, _ := io.ReadAll(r.Body)
	var req respRequest
	if err := json.Unmarshal(b, &req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	item := f.next(req)
	w.Header().Set("Content-Type", "text/event-stream")
	event := func(kind string, v map[string]any) {
		v["type"] = kind
		data, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data)
	}
	event("response.created", map[string]any{"response": map[string]any{"id": "resp"}})
	event("response.output_item.done", map[string]any{"item": item})
	event("response.completed", map[string]any{"response": map[string]any{"id": "resp",
		"usage": map[string]any{"input_tokens": 100, "output_tokens": 10, "total_tokens": 110}}})
}

// CodexHome is a Codex folder whose model is this API, which needs no login.
func (f *fakeResponses) CodexHome(t *testing.T) string {
	dir := HomeTemp(t)
	cfg := "model_provider = \"fake\"\nmodel = \"gpt-5\"\n[model_providers.fake]\nname = \"fake\"\nbase_url = " +
		quoteJSON(f.URL) + "\nwire_api = \"responses\"\nrequires_openai_auth = false\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func (f *fakeResponses) Log() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.logs, "\n")
}

type respRequest struct {
	Input []respItem `json:"input"`
	Tools []struct {
		Name  string `json:"name"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	} `json:"tools"`
}

type respItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	CallID    string          `json:"call_id"`
	Output    json.RawMessage `json:"output"`
}

func (i respItem) text() string {
	var s string
	if json.Unmarshal(i.Content, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(i.Content, &parts)
	var out []string
	for _, p := range parts {
		out = append(out, p.Text)
	}
	return strings.Join(out, "\n")
}

func (i respItem) output() string {
	var s string
	if json.Unmarshal(i.Output, &s) == nil {
		return s
	}
	return string(i.Output)
}

var (
	shellLine = regexp.MustCompile(`(?m)Run exactly this shell command once: (.+)$`)
	skillLine = regexp.MustCompile(`(?m)Read your skill ([\w-]+)`)
	askLine   = regexp.MustCompile(`(?m)Ask the owner with the ask tool[^"]*"([^"]+)"`)
	wordIn    = regexp.MustCompile(`"word": "([^"]*)"`)
)

// next is the one output item of the model for the request: the next step of the last prompt not done yet.
func (f *fakeResponses) next(req respRequest) map[string]any {
	last, all := -1, ""
	for n, it := range req.Input {
		all += it.text() + "\n" + it.output() + "\n" + it.Arguments + "\n"
		if it.Type == "message" && it.Role == "user" {
			last = n
		}
	}
	if last < 0 {
		return f.say("no prompt")
	}
	prompt := req.Input[last].text()
	var done []respItem
	outputs := map[string]string{}
	for _, it := range req.Input[last+1:] {
		switch it.Type {
		case "function_call":
			done = append(done, it)
		case "function_call_output":
			outputs[it.CallID] = it.output()
			f.mu.Lock()
			if !f.seen[it.CallID] {
				f.seen[it.CallID] = true
				f.logs = append(f.logs, "  -> "+strings.ReplaceAll(it.output(), "\n", " | "))
			}
			f.mu.Unlock()
		}
	}
	tool := func(name string) string {
		for _, t := range req.Tools {
			for _, in := range t.Tools {
				if in.Name == name {
					return t.Name + "." + name
				}
			}
		}
		f.mu.Lock()
		f.logs = append(f.logs, "no tool "+name)
		f.mu.Unlock()
		return name
	}
	i, answer := 0, ""
	type step struct{ kind, arg string }
	var steps []step
	for _, line := range strings.Split(prompt, "\n") {
		switch {
		case shellLine.MatchString(line):
			steps = append(steps, step{"shell", shellLine.FindStringSubmatch(line)[1]})
		case skillLine.MatchString(line):
			steps = append(steps, step{"skill", skillLine.FindStringSubmatch(line)[1]})
		case askLine.MatchString(line):
			steps = append(steps, step{"ask", askLine.FindStringSubmatch(line)[1]})
		case strings.Contains(line, "result_submit"):
			steps = append(steps, step{"submit", line})
		}
	}
	for _, s := range steps {
		switch s.kind {
		case "shell":
			if i < len(done) {
				i++
				continue
			}
			return f.call("exec_command", map[string]any{"cmd": s.arg})
		case "skill":
			if i == len(done) {
				m := regexp.MustCompile(`\(file: (r\d+)(/` + regexp.QuoteMeta(s.arg) + `/SKILL\.md)\)`).FindStringSubmatch(all)
				if m == nil {
					return f.say("no skill " + s.arg)
				}
				root := regexp.MustCompile("`" + m[1] + "` = `([^`]+)`").FindStringSubmatch(all)
				if root == nil {
					return f.say("no root " + m[1])
				}
				return f.call("exec_command", map[string]any{"cmd": "cat " + root[1] + m[2] + " 2>&1"})
			}
			read := outputs[done[i].CallID]
			i++
			if i < len(done) {
				i++
				continue
			}
			m := shellLine.FindStringSubmatch(read)
			if m == nil {
				return f.say("the skill did not read: " + read)
			}
			return f.call("exec_command", map[string]any{"cmd": m[1]})
		case "ask":
			if i < len(done) {
				answer = outputs[done[i].CallID]
				i++
				continue
			}
			return f.call(tool("ask"), map[string]any{"question": s.arg})
		case "submit":
			if i < len(done) {
				i++
				continue
			}
			if strings.Contains(prompt, "same word and answer as before") {
				return f.call(tool("result_submit"), json.RawMessage(f.submitted(req.Input[:last])))
			}
			word := ""
			if m := wordIn.FindStringSubmatch(s.arg); m != nil {
				word = m[1]
			}
			if m := regexp.MustCompile(`The owner answered: ([\w-]+)`).FindStringSubmatch(answer); m != nil {
				answer = m[1]
			}
			return f.call(tool("result_submit"), map[string]any{"result": map[string]any{"word": word, "answer": answer}})
		}
	}
	return f.say("done")
}

// submitted are the arguments of the last result_submit before the prompt.
func (f *fakeResponses) submitted(items []respItem) string {
	for n := len(items) - 1; n >= 0; n-- {
		if items[n].Type == "function_call" && items[n].Name == "result_submit" {
			return items[n].Arguments
		}
	}
	return "{}"
}

func (f *fakeResponses) call(name string, args any) map[string]any {
	b, _ := json.Marshal(args)
	f.mu.Lock()
	f.logs = append(f.logs, name+" "+string(b))
	id := fmt.Sprintf("call_%d", len(f.logs))
	f.mu.Unlock()
	item := map[string]any{"type": "function_call", "id": "fc_" + id, "call_id": id, "name": name, "arguments": string(b)}
	if space, bare, ok := strings.Cut(name, "."); ok {
		item["namespace"], item["name"] = space, bare
	}
	return item
}

func (f *fakeResponses) say(text string) map[string]any {
	f.mu.Lock()
	f.logs = append(f.logs, "say "+text)
	f.mu.Unlock()
	return map[string]any{"type": "message", "role": "assistant", "id": "msg", "content": []any{map[string]any{"type": "output_text", "text": text}}}
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// HomeTemp is a folder of the test in the home, as deck's and Codex's are: Codex puts no helper of its sandbox under
// /tmp, and on Linux its sandbox shows /tmp only where it may write; with a user of agents it is in theirs, as theirs.
func HomeTemp(t *testing.T) string {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	agent, _ := user.Lookup(os.Getenv("DECK_AGENT_USER"))
	if agent != nil {
		home = agent.HomeDir
	}
	dir, err := os.MkdirTemp(home, ".deck-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if agent != nil {
		uid, _ := strconv.Atoi(agent.Uid)
		gid, _ := strconv.Atoi(agent.Gid)
		if err := os.Chown(dir, uid, gid); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
