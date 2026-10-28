package scenarios

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// codexSchema is the part of the Codex protocol schema the adapter uses, with the methods and their params.
type codexSchema struct {
	Methods     map[string]map[string]string `json:"methods"`
	Definitions map[string]struct {
		Properties map[string]json.RawMessage `json:"properties"`
	} `json:"definitions"`
}

// codexArgs are the config keys deck may set when it starts the app-server.
var codexArgs = []string{"features.", "agents.enabled=", "notify=", "web_search=", "default_permissions=", "permissions.deck.", "shell_environment_policy.set="}

// codexThreadConfig are the keys deck may set in the config of a thread.
var codexThreadConfig = []string{"mcp_servers", "skills"}

// serveCodex is the fake app-server: it rejects arguments, methods and fields the real schema lacks, records what
// deck sent and replays stream.<n>: replies by method, messages to send and its own acts (mcp, shell, hang).
func serveCodex(dir string) {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		v, err := os.ReadFile(filepath.Join(dir, "version"))
		if err != nil {
			v = []byte("0.155.1")
		}
		fmt.Printf("codex-cli %s\n", strings.TrimSpace(string(v)))
		os.Exit(0)
	}
	srv := newFakeServer("fake codex", dir, args)
	if len(args) == 0 || args[0] != "app-server" {
		srv.violate("unexpected arguments %q", args)
	}
	for i := 1; i < len(args); i += 2 {
		if args[i] != "-c" || i+1 >= len(args) || !slices.ContainsFunc(codexArgs, func(p string) bool { return strings.HasPrefix(args[i+1], p) }) {
			srv.violate("unexpected argument %q", args[i])
		}
	}
	b, err := os.ReadFile(filepath.Join(os.Getenv("DECK_FAKE_CODEX_ROOT"), "..", "..", "internal", "provider", "codex", "protocol.schema.json"))
	if err != nil {
		srv.violate("no schema: %v", err)
	}
	var schema codexSchema
	if err := json.Unmarshal(b, &schema); err != nil {
		srv.violate("schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		srv.violate("schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft7)
	if err := compiler.AddResource("mem://codex.json", doc); err != nil {
		srv.violate("schema: %v", err)
	}
	f := &fakeCodexRun{fakeServer: srv, schema: schema, compiler: compiler}
	f.run(f.stream())
}

type fakeCodexRun struct {
	*fakeServer
	schema       codexSchema
	compiler     *jsonschema.Compiler
	deckURL      string
	thread, turn string
	// deafAfter stops reading stdin after a request of this method until a flood of output is out.
	deafAfter atomic.Value
	hear      chan struct{}
}

var (
	fakeWord = regexp.MustCompile(`deck-check-[0-9a-f]+`)
	fakeKey  = regexp.MustCompile(`cat (/\S+key\.txt)`)
)

func (f *fakeCodexRun) listen() {
	f.read(f.check, func(m map[string]json.RawMessage) {
		if w, _ := f.deafAfter.Load().(string); w != "" && string(m["method"]) == strconv.Quote(w) {
			<-f.hear
		}
	})
}

// check rejects a method or a field the schema does not have.
func (f *fakeCodexRun) check(m map[string]json.RawMessage) {
	var method string
	if json.Unmarshal(m["method"], &method) != nil {
		return
	}
	params, ok := f.schema.Methods["ClientRequest"][method]
	if !ok {
		params, ok = f.schema.Methods["ClientNotification"][method]
	}
	if !ok {
		f.violate("unknown method %s", method)
	}
	if params == "" || len(m["params"]) == 0 {
		return
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(m["params"], &fields)
	props := f.schema.Definitions[params].Properties
	for k := range fields {
		if _, ok := props[k]; !ok {
			f.violate("unknown field %s of %s", k, method)
		}
	}
	sch, err := f.compiler.Compile("mem://codex.json#/definitions/" + params)
	if err != nil {
		f.violate("schema of %s: %v", params, err)
	}
	v, _ := jsonschema.UnmarshalJSON(bytes.NewReader(m["params"]))
	if err := sch.Validate(v); err != nil {
		f.violate("params of %s do not match the schema: %v", method, err)
	}
	var cfg map[string]json.RawMessage
	if json.Unmarshal(fields["config"], &cfg) == nil {
		for k := range cfg {
			if !slices.Contains(codexThreadConfig, k) {
				f.violate("unexpected thread config %s", k)
			}
		}
	}
	if method == "turn/start" {
		if w := fakeWord.FindString(string(m["params"])); w != "" {
			_ = os.WriteFile(filepath.Join(f.dir, "word"), []byte(w), 0o644)
		}
		if k := fakeKey.FindStringSubmatch(string(m["params"])); k != nil {
			_ = os.WriteFile(filepath.Join(f.dir, "key"), []byte(k[1]), 0o644)
		}
	}
}

// subst fills in the word of a self-check, the hidden key it names and the folder of the call.
func (f *fakeCodexRun) subst(s string) string {
	w, _ := os.ReadFile(filepath.Join(f.dir, "word"))
	key, _ := os.ReadFile(filepath.Join(f.dir, "key"))
	secret, _ := os.ReadFile(strings.TrimSpace(string(key)))
	skills, _ := os.ReadFile(filepath.Join(f.dir, "skills"))
	return strings.NewReplacer("{{word}}", strings.TrimSpace(string(w)), "{{secret}}", strings.TrimSpace(string(secret)), "{{cwd}}", f.cwd,
		"{{skill}}", skillCommand(string(skills))).Replace(s)
}

// skillCommand is the command the skill of a self-check in the skills folder gives.
func skillCommand(skills string) string {
	b, _ := os.ReadFile(filepath.Join(skills, "deck-check", "SKILL.md"))
	if m := regexp.MustCompile(`Run exactly this shell command once: (.+)`).FindStringSubmatch(string(b)); m != nil {
		return m[1]
	}
	return "true"
}

func (f *fakeCodexRun) run(lines []string) {
	go f.listen()
	for _, raw := range lines {
		var l map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &l) != nil {
			f.violate("bad stream line %s", raw)
		}
		switch {
		case l["reply"] != nil:
			var want string
			_ = json.Unmarshal(l["reply"], &want)
			f.reply(want, l)
		case l["mcp"] != nil:
			f.callTool("codex-mcp-client", f.deckURL, json.RawMessage(f.subst(string(l["mcp"]))))
		case l["shell"] != nil:
			var cmd string
			_ = json.Unmarshal(l["shell"], &cmd)
			_ = exec.Command("sh", "-c", f.subst(cmd)).Run()
		case l["deafAfter"] != nil:
			var method string
			_ = json.Unmarshal(l["deafAfter"], &method)
			f.hear = make(chan struct{})
			f.deafAfter.Store(method)
		case l["flood"] != nil:
			var n int
			_ = json.Unmarshal(l["flood"], &n)
			for i := range n {
				f.write(fmt.Sprintf(`{"method":"item/agentMessage/delta","params":{"threadId":"other","turnId":"t","itemId":"i%d","delta":%q}}`, i, strings.Repeat("x", 500)))
			}
			close(f.hear)
		case l["hang"] != nil:
			for range f.in {
			}
			select {}
		case l["deaf"] != nil:
			signal.Ignore(syscall.SIGTERM)
			_ = os.Stdout.Close()
			select {}
		case l["id"] != nil:
			f.write(f.subst(raw))
			f.record("answers.jsonl", f.answer(string(l["id"]), "server request "+string(l["id"])))
		default:
			f.write(f.subst(raw))
		}
	}
}

// reply answers the next request, which must be the one the stream expects.
func (f *fakeCodexRun) reply(want string, l map[string]json.RawMessage) {
	for {
		m := f.nextRequest(nil)
		var method string
		_ = json.Unmarshal(m["method"], &method)
		if method == "skills/extraRoots/set" {
			var roots struct {
				ExtraRoots []string `json:"extraRoots"`
			}
			if json.Unmarshal(m["params"], &roots) == nil && len(roots.ExtraRoots) > 0 {
				_ = os.WriteFile(filepath.Join(f.dir, "skills"), []byte(roots.ExtraRoots[0]), 0o644)
			}
		}
		if method == "skills/extraRoots/set" && want != method {
			f.write(`{"id":` + string(m["id"]) + `,"result":{}}`)
			continue
		}
		if method == "turn/interrupt" && want != method {
			f.write(`{"id":` + string(m["id"]) + `,"result":{}}`)
			f.write(fmt.Sprintf(`{"method":"turn/completed","params":{"threadId":%q,"turn":{"id":%q,"items":[],"status":"interrupted"}}}`, f.thread, f.turn))
			os.Exit(0)
		}
		if method != want {
			f.violate("expected %s, got %s", want, method)
		}
		if method == "thread/start" || method == "thread/resume" {
			var p struct {
				Config struct {
					MCPServers map[string]struct {
						URL string `json:"url"`
					} `json:"mcp_servers"`
				} `json:"config"`
			}
			_ = json.Unmarshal(m["params"], &p)
			f.deckURL = p.Config.MCPServers["deck"].URL
		}
		out := map[string]json.RawMessage{"id": m["id"]}
		if l["error"] != nil {
			out["error"] = l["error"]
		} else {
			out["result"] = l["result"]
			var r struct {
				Thread struct{ ID string }
				Turn   struct{ ID string }
			}
			_ = json.Unmarshal(l["result"], &r)
			if r.Thread.ID != "" {
				f.thread = r.Thread.ID
			}
			if r.Turn.ID != "" {
				f.turn = r.Turn.ID
			}
		}
		b, _ := json.Marshal(out)
		f.write(f.subst(string(b)))
		return
	}
}
