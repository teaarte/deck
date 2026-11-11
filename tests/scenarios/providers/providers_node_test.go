package providers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/provider/remote"
	"deck2/tests/harness"
)

func hasModel(e *harness.Env, name string) bool {
	var out struct{ Models []struct{ Name, Node string } }
	e.Must("deck.models_list", map[string]any{}, &out)
	for _, m := range out.Models {
		if m.Name == name && m.Node == "Mac" {
			return true
		}
	}
	return false
}

// TestAnAgentOnANodeAsksAndHandsItsResultInThroughDeck: codex@Mac asks and hands its result in through deck from the
// node's own folder; its files come back to deck, and no key of deck is in its command line or environment.
func TestAnAgentOnANodeAsksAndHandsItsResultInThroughDeck(t *testing.T) {
	own, onNode := harness.NewCodex(t), harness.NewCodex(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	raw, err := os.ReadFile(harness.Testdata("codex/ask.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	last := lines[len(lines)-1]
	lines = append(lines[:len(lines)-1:len(lines)-1], `{"shell": "echo blue > report.txt && mkdir -p ../results && echo drawn > ../results/picture.txt"}`, last)
	onNode.StreamText(1, strings.Join(lines, "\n"))
	e := harness.New(t, harness.WithCodex(own))
	n := e.PairNode("Mac")
	n.RunAgents(func(spec remote.Spec, home string, _ func(provider.ModelHooks) http.Handler) provider.Provider {
		return onNode.Adapter(home)
	})
	n.Allow("codex")
	n.Start()
	e.Until("codex@Mac among the agents", func() bool { return hasModel(e, "codex@Mac") })
	p := e.Project("paint", harness.ColorManifest)
	e.Secret("paint", "PAINT_KEY", "secret-of-paint")
	e.UseModels("paint.work", "codex@Mac")
	id := e.Run(p, "work")
	a := e.WaitAsk()
	if a.Kind != "question" || !strings.Contains(a.Text, "Which color should the report use?") {
		t.Fatalf("the question of the agent on the node reaches the owner: %+v", a)
	}
	e.Answer(a.ID, "blue")
	got := e.WaitState(id, "done", "error")
	cc := onNode.Call(1)
	if got.State != "done" || string(got.Result) != `{"color":"blue"}` || cc.Violation != "" {
		t.Fatalf("the result of the agent on the node comes in by result_submit through deck: %+v %s", got, cc.Violation)
	}
	if len(cc.Mcp) != 2 || !strings.Contains(cc.Mcp[0]["text"].(string), "blue") || cc.Mcp[1]["is_error"] != false {
		t.Fatalf("the agent on the node got the answer and deck took its result: %v", cc.Mcp)
	}
	var proc struct{ Provider string }
	e.Must("deck.process_get", map[string]any{"process": id}, &proc)
	if proc.Provider != "codex@Mac" {
		t.Fatalf("the process ran on the agent of the node: %+v", proc)
	}
	if _, err := os.Stat(filepath.Join(own.Dir, "calls")); err == nil {
		t.Fatal("deck's own Codex took no turn")
	}
	nodeHome, _ := filepath.EvalSymlinks(n.Home)
	if !strings.HasPrefix(strings.TrimSpace(cc.Cwd), nodeHome) {
		t.Fatalf("the agent works in the node's own folder: %s", cc.Cwd)
	}
	h := deckhome.Layout(e.Home)
	if b, err := os.ReadFile(filepath.Join(h.RunWork(id), "report.txt")); err != nil || strings.TrimSpace(string(b)) != "blue" {
		t.Fatalf("the file the agent wrote on the node is on deck after the turn: %q %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(h.Results(id), "picture.txt")); err != nil || strings.TrimSpace(string(b)) != "drawn" {
		t.Fatalf("a result the agent left on the node is on deck after the turn: %q %v", b, err)
	}
	if _, err := os.Stat(deckhome.Layout(n.Home).Mirror(e.Session(id))); err == nil {
		t.Fatal("the node keeps no files of a turn deck took back")
	}
	everything := strings.Join(cc.Args, "\n")
	for k, v := range cc.Env {
		everything += "\n" + k + "=" + v
	}
	for _, secret := range []string{n.Joined.Key, "secret-of-paint", "/mcp/"} {
		if strings.Contains(everything, secret) {
			t.Fatalf("%q is in the command line or environment of the agent on the node", secret)
		}
	}
}

// claudeOnNode is a deck on the fake Claude Code and a node Mac that runs claude-code on a fake of its own, its turns
// waiting wait for a node gone away.
func claudeOnNode(t *testing.T, wait time.Duration) (e *harness.Env, own, onNode *harness.FakeCLI, n *harness.Node) {
	own, onNode = harness.NewCLI(t), harness.NewCLI(t)
	own.Stream(0, "ok.jsonl")
	onNode.Stream(0, "ok.jsonl")
	e = harness.New(t, harness.WithCLI(own), harness.WithConfig(func(c *app.Config) { c.NodeWait = wait }))
	n = e.PairNode("Mac")
	n.RunAgents(func(spec remote.Spec, home string, _ func(provider.ModelHooks) http.Handler) provider.Provider {
		return onNode.Adapter(home)
	})
	n.Allow("claude-code")
	n.Start()
	e.Until("claude-code@Mac among the agents", func() bool { return hasModel(e, "claude-code@Mac") })
	return e, own, onNode, n
}

// TestAnAgentWhoseNodeWentAwayWaitsThenGoesToTheNextAgent: the node of a turn goes away in the middle of it; the turn
// waits for it, then its work goes on on the next agent of the assignment, and health says the node is offline.
func TestAnAgentWhoseNodeWentAwayWaitsThenGoesToTheNextAgent(t *testing.T) {
	t.Parallel()
	e, own, onNode, n := claudeOnNode(t, 2*time.Second)
	onNode.Before(`sleep 60`)
	p := e.Project("plain", harness.PlainManifest)
	e.UseModels("plain.work", "claude-code@Mac", "claude-code")
	id := e.Run(p, "work")
	e.Until("the turn to start on the node", func() bool { return onNode.Calls() == 1 })
	n.Stop()
	e.WaitState(id, "error")
	if f := e.Fault(id); f.ErrorType != "model.unavailable" || !strings.Contains(f.Error, "node Mac went away during the turn") ||
		!strings.Contains(f.Error, "did not come back in 2s") {
		t.Fatalf("the turn waits for its node, then fails as a model that did not answer: %+v", f)
	}
	next := e.Following(id)
	var in struct{ Provider string }
	e.Must("deck.process_get", map[string]any{"process": next}, &in)
	if got := e.WaitState(next, "done", "error"); got.State != "done" || in.Provider != "claude-code" || own.Calls() != 1 {
		t.Fatalf("the next agent of the assignment goes on with the work: %+v %+v", got, in)
	}
	e.WaitNote("claude-code goes on with it")
	if h := e.WaitHealth("node:Mac", "warning"); !strings.Contains(h.Error, "node Mac is offline since") {
		t.Fatalf("health says the node is offline: %+v", h)
	}
	if hasModel(e, "claude-code@Mac") {
		t.Fatal("the agent of a node that is offline is not offered")
	}
}

// TestAnAgentWhoseNodeComesBackGoesOnInItsSession: a node gone in the middle of a turn comes back in time; the turn
// goes on there in its session, as a turn killed from outside does.
func TestAnAgentWhoseNodeComesBackGoesOnInItsSession(t *testing.T) {
	t.Parallel()
	e, own, onNode, n := claudeOnNode(t, time.Minute)
	onNode.Before(`[ "$n" = 1 ] && { echo cut > before-the-cut.txt; sleep 60; }`)
	p := e.Project("plain", harness.PlainManifest)
	e.UseModels("plain.work", "claude-code@Mac", "claude-code")
	id := e.Run(p, "work")
	cut := filepath.Join(deckhome.Layout(n.Home).Mirror(e.Session(id)), "work", "runs", harness.Itoa(id), "work", "before-the-cut.txt")
	e.Until("the turn on the node to write its first file", func() bool { _, err := os.Stat(cut); return err == nil })
	n.Stop()
	e.WaitNote("the turn of claude-code@Mac waits up to 1m0s for it")
	n.Start()
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("the turn goes on once its node is back: %+v %+v", got, e.Fault(id))
	}
	if onNode.Calls() != 2 || own.Calls() != 0 {
		t.Fatalf("the turn went on on the node, not on deck: %d %d", onNode.Calls(), own.Calls())
	}
	if s, ok := harness.Flag(onNode.Call(2).Args, "--resume"); !ok || s != e.Session(id) {
		t.Fatalf("the turn goes on in its session: %v", onNode.Call(2).Args)
	}
	if b, err := os.ReadFile(filepath.Join(deckhome.Layout(e.Home).RunWork(id), "before-the-cut.txt")); err != nil || string(b) != "cut\n" {
		t.Fatalf("what the turn did on the node before it went away stays: %q %v", b, err)
	}
}

// TestAnACPAgentOnANodeCallsItsModelThroughDeck: an ACP agent runs on the node, while its model is deck's: each call
// goes through deck's proxy, which puts the key in and journals it, and the result comes in through deck.
func TestAnACPAgentOnANodeCallsItsModelThroughDeck(t *testing.T) {
	own, onNode, llm := harness.NewACP(t), harness.NewACP(t), harness.AcpLLM(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	e := harness.New(t, harness.WithACP(own, llm))
	n := e.PairNode("Mac")
	n.RunAgents(func(spec remote.Spec, home string, api func(provider.ModelHooks) http.Handler) provider.Provider {
		a, err := acp.New(acp.Config{Name: spec.Name, Agent: spec.Agent, Binary: filepath.Join(onNode.Dir, "goose"), Model: spec.Model,
			CallLimit: spec.CallLimit, API: api, Home: deckhome.Layout(home), Sandbox: harness.FakeBox(home, onNode.Dir)})
		if err != nil {
			t.Fatal(err)
		}
		return a
	})
	n.Allow("goose")
	n.Start()
	e.Until("acp@Mac among the agents", func() bool { return hasModel(e, "acp@Mac") })
	onNode.StreamText(1, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"shell": "echo painted > canvas.txt"}`, harness.AcpModel,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	p := e.Project("paint", harness.ColorManifest)
	e.UseModels("paint.work", "acp@Mac")
	before := len(llm.Requests())
	id := e.Run(p, "work")
	got := e.WaitState(id, "done", "error")
	c := onNode.Call(1)
	if got.State != "done" || string(got.Result) != `{"color":"blue"}` || c.Violation != "" {
		t.Fatalf("the result of the agent on the node comes in through deck: %+v %+v %s", got, e.Fault(id), c.Violation)
	}
	reqs := llm.Requests()
	if len(reqs) != before+1 || reqs[len(reqs)-1].Auth != "Bearer sk-real-model-key" || len(c.Model) != 1 || c.Model[0]["status"] != 200.0 {
		t.Fatalf("the model call of the agent on the node goes through deck's proxy, which puts the key in: %v %v", reqs, c.Model)
	}
	for k, v := range c.Env {
		if strings.Contains(v, "sk-real-model-key") {
			t.Fatalf("the agent on the node sees no key of the model: %s", k)
		}
	}
	var calls []map[string]any
	e.ModelCalls("", id, &calls)
	if len(calls) != 1 || calls[0]["gen_ai.provider.name"] != "acp@Mac" {
		t.Fatalf("deck journals the model call of the agent on the node: %v", calls)
	}
	if b, err := os.ReadFile(filepath.Join(deckhome.Layout(e.Home).RunWork(id), "canvas.txt")); err != nil || string(b) != "painted\n" {
		t.Fatalf("the file of the agent on the node is on deck: %q %v", b, err)
	}
}

// TestTheCLIOfANodeSignsInByALinkFromDeck: the sign-in of the CLI of a node starts from deck, its link comes to the
// owner and its code goes back, but the CLI that signs in is the node's.
func TestTheCLIOfANodeSignsInByALinkFromDeck(t *testing.T) {
	t.Parallel()
	e, own, onNode, _ := claudeOnNode(t, time.Minute)
	var l struct {
		URL   string
		Paste bool
	}
	e.Must("deck.agent_login", map[string]any{"provider": "claude-code@Mac"}, &l)
	if !strings.Contains(l.URL, "oauth/authorize") || !l.Paste {
		t.Fatalf("the link of the sign-in on the node comes to the owner: %+v", l)
	}
	e.Must("deck.agent_login_finish", map[string]any{"provider": "claude-code@Mac", "code": "good-code"}, nil)
	e.Until("the CLI of the node to sign in", func() bool {
		_, err := os.Stat(filepath.Join(onNode.Dir, "signed-in"))
		return err == nil
	})
	if _, err := os.Stat(filepath.Join(own.Dir, "signed-in")); err == nil {
		t.Fatal("the CLI of deck's own machine did not sign in")
	}
}

// TestFilesFromANodeNeverGoThroughALinkOnDeck: a link a program of the project left in its data leads into deck's
// state; what the agent on the node writes under that name never reaches deck's state.
func TestFilesFromANodeNeverGoThroughALinkOnDeck(t *testing.T) {
	t.Parallel()
	e, _, onNode, _ := claudeOnNode(t, time.Minute)
	p := e.Project("plain", harness.PlainManifest)
	h := deckhome.Layout(e.Home)
	if err := os.MkdirAll(h.Data(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../../state", filepath.Join(h.Data(p), "x")); err != nil {
		t.Fatal(err)
	}
	onNode.Before(`mkdir -p ../../../projects/` + harness.Itoa(p) + `/data/x && echo evil > ../../../projects/` + harness.Itoa(p) + `/data/x/evil.txt`)
	e.UseModels("plain.work", "claude-code@Mac")
	id := e.Run(p, "work")
	e.WaitState(id, "done", "error")
	if _, err := os.Stat(filepath.Join(h.State(), "evil.txt")); err == nil {
		t.Fatal("a file of the node went through a link into deck's state")
	}
	said := false
	for _, l := range e.Log(id) {
		said = said || strings.Contains(fmt.Sprint(l["text"]), "goes through a link")
	}
	if !said {
		t.Fatalf("the turn says which file did not come back: %v", e.Log(id))
	}
}

// TestAMastersTurnOnANodeSeesTheFilesOfAMessage: a picture the owner attaches to a message goes to the node with the
// turn, at the path the prompt names there.
func TestAMastersTurnOnANodeSeesTheFilesOfAMessage(t *testing.T) {
	own, onNode := harness.NewCodex(t), harness.NewCodex(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	e := harness.New(t, harness.WithCodex(own))
	n := e.PairNode("Mac")
	n.RunAgents(func(spec remote.Spec, home string, _ func(provider.ModelHooks) http.Handler) provider.Provider {
		return onNode.Adapter(home)
	})
	n.Allow("codex")
	n.Start()
	e.Until("codex@Mac among the agents", func() bool { return hasModel(e, "codex@Mac") })
	e.UseModels("master.reply", "codex@Mac")
	shot := "\x89PNG\r\n\x1a\n a screenshot"
	id := e.Upload("master", "image.png", "image/png", shot)
	raw, err := os.ReadFile(harness.Testdata("codex/reply.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	last := lines[len(lines)-1]
	lines = append(lines[:len(lines)-1:len(lines)-1], fmt.Sprintf(`{"shell": "wc -c < ../input/%d-image.png > seen.txt"}`, id), last)
	onNode.StreamText(1, strings.Join(lines, "\n"))
	topic := e.Say(0, "look at this", map[string]any{"files": []string{fmt.Sprint(id)}})
	e.MasterSaid(topic, 1)
	cc := onNode.Call(1)
	var input []struct{ Text string }
	_ = json.Unmarshal(cc.Request("turn/start")["input"], &input)
	at := filepath.Join(filepath.Dir(strings.TrimSpace(cc.Cwd)), "input", fmt.Sprintf("%d-image.png", id))
	named := regexp.MustCompile(`read-only: (\S+?\.png)`).FindStringSubmatch(fmt.Sprint(input))
	if named == nil {
		t.Fatalf("the prompt names the picture: %+v", input)
	}
	nodeHome, _ := filepath.EvalSymlinks(n.Home)
	if told, _ := filepath.Rel(n.Home, named[1]); filepath.Join(nodeHome, told) != at {
		t.Fatalf("the prompt on the node names the picture where it is there, %s: %s", at, named[1])
	}
	pid := e.RunsOf("master", "reply")[0].ID
	if b, err := os.ReadFile(filepath.Join(deckhome.Layout(e.Home).RunWork(pid), "seen.txt")); err != nil || strings.TrimSpace(string(b)) != fmt.Sprint(len(shot)) {
		t.Fatalf("the turn on the node read the picture: %q %v", b, err)
	}
}

// TestAnAnswerWithAPictureReachesATurnOnANodeWithItsNextTurn: the owner answers the question of the master's turn on
// a node with a picture; the answer names no path the node lacks, and the picture comes with the next turn.
func TestAnAnswerWithAPictureReachesATurnOnANodeWithItsNextTurn(t *testing.T) {
	own, onNode := harness.NewCodex(t), harness.NewCodex(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	e := harness.New(t, harness.WithCodex(own))
	n := e.PairNode("Mac")
	n.RunAgents(func(spec remote.Spec, home string, _ func(provider.ModelHooks) http.Handler) provider.Provider {
		return onNode.Adapter(home)
	})
	n.Allow("codex")
	n.Start()
	e.Until("codex@Mac among the agents", func() bool { return hasModel(e, "codex@Mac") })
	e.UseModels("master.reply", "codex@Mac")
	shot := "\x89PNG\r\n\x1a\n a screenshot"
	id := e.Upload("master", "image.png", "image/png", shot)
	stream := func(name string, more ...string) string {
		raw, err := os.ReadFile(harness.Testdata("codex/" + name))
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if !strings.Contains(l, "result_submit") {
				lines = append(lines, l)
			}
		}
		last := lines[len(lines)-1]
		return strings.Join(append(append(lines[:len(lines)-1:len(lines)-1], more...), last), "\n")
	}
	onNode.StreamText(1, stream("ask.jsonl"))
	onNode.StreamText(2, stream("resume.jsonl", fmt.Sprintf(`{"shell": "wc -c < ../input/%d-image.png > seen.txt"}`, id)))
	topic := e.Say(0, "make a report")
	e.WaitAsk()
	e.Say(topic, "blue, like this", map[string]any{"files": []string{fmt.Sprint(id)}})
	e.Until("the turn after the answer", func() bool {
		rs := e.RunsOf("master", "reply")
		return len(rs) == 1 && rs[0].State != "running" && len(onNode.Call(2).Cwd) > 0
	})
	answer := fmt.Sprint(onNode.Call(1).Mcp)
	if !strings.Contains(answer, "blue, like this") || !strings.Contains(answer, "come with your next turn") || strings.Contains(answer, "/input/") {
		t.Fatalf("the answer on the node names no path it lacks: %s", answer)
	}
	cc := onNode.Call(2)
	var input []struct{ Text string }
	_ = json.Unmarshal(cc.Request("turn/start")["input"], &input)
	at := filepath.Join(filepath.Dir(strings.TrimSpace(cc.Cwd)), "input", fmt.Sprintf("%d-image.png", id))
	named := regexp.MustCompile(`read-only: (\S+?\.png)`).FindStringSubmatch(fmt.Sprint(input))
	nodeHome, _ := filepath.EvalSymlinks(n.Home)
	if named == nil {
		t.Fatalf("the next turn names the picture: %+v", input)
	}
	if told, _ := filepath.Rel(n.Home, named[1]); filepath.Join(nodeHome, told) != at {
		t.Fatalf("the next turn on the node names the picture where it is there, %s: %s", at, named[1])
	}
	pid := e.RunsOf("master", "reply")[0].ID
	e.Until("the turn's files back on deck", func() bool {
		b, err := os.ReadFile(filepath.Join(deckhome.Layout(e.Home).RunWork(pid), "seen.txt"))
		return err == nil && strings.TrimSpace(string(b)) == fmt.Sprint(len(shot))
	})
}
