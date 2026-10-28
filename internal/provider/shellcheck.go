package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"deck2/internal/sandbox"
	"deck2/internal/untrusted"
)

// toolWord is tools as a word, not a field of a message such as tool_call_id.
var toolWord = regexp.MustCompile(`(?i)\btools?\b`)

// noTools says a refusal of a model API is that it takes no tools, not a field of what the agent sent.
func noTools(refusal string) bool {
	return toolWord.MatchString(refusal) && strings.Contains(strings.ToLower(refusal), "support")
}

// checkSkill is the skill a self-check gives its turn.
const checkSkill = "deck-check"

// ShellCheck is what the self-check of an adapter whose agent has a shell of its own differs in.
type ShellCheck struct {
	// Agent names the agent in what the check says.
	Agent string
	// Dir keeps the folders of the check while it runs.
	Dir string
	Run func(ctx context.Context, req Request) (Result, error)
	// PermissionMCP is the gateway's permission tool, for an agent that asks deck about its own approvals.
	PermissionMCP string
	// Model marks what the model failed to do apart from faults of the adapter, and measures its speed.
	Model bool
	// Done removes what the adapter kept of a session of the check.
	Done func(session string)
}

// Check runs a short session as deck runs agents: a write, a skill, a hidden file its shell cannot read, ask, result_submit,
// token counts, a resume, then a turn whose curl has no network (not in deck's container); deck reads the probes.
func (s ShellCheck) Check(ctx context.Context, start Check) (c Check) {
	c = start
	word, secret, skilled := CheckWord(), CheckWord(), CheckWord()
	answer := word + "-answered"
	cs := &CheckServer{Answer: answer, Approve: s.PermissionMCP != ""}
	url, stop, err := ServeCheck(cs.Server())
	if err != nil {
		c.Err = err
		return c
	}
	defer stop()
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		c.Err = err
		return c
	}
	dir, err := os.MkdirTemp(s.Dir, "selfcheck-")
	if err != nil {
		c.Err = err
		return c
	}
	defer os.RemoveAll(dir)
	work, hidden := filepath.Join(dir, "work"), filepath.Join(dir, "secrets")
	key := filepath.Join(hidden, "key.txt")
	probe := "cd " + Quote(work) + "; cat " + Quote(key) + " > probe.out 2>&1; echo $? > probe.code\n"
	netProbe := "cd " + Quote(work) + "; curl -s -m 5 -o /dev/null -w '%{http_code}' https://example.com > net.out 2>/dev/null\n"
	if err := errors.Join(os.Mkdir(work, 0o700), os.Mkdir(hidden, 0o700), os.WriteFile(key, []byte(secret+"\n"), 0o600),
		os.WriteFile(filepath.Join(work, "probe.sh"), []byte(probe), 0o600), os.WriteFile(filepath.Join(work, "net.sh"), []byte(netProbe), 0o600)); err != nil {
		c.Err = err
		return c
	}
	var tokens, output int64
	var spent time.Duration
	var mu sync.Mutex
	model := func(err error) { c.Err, c.ModelFailed = err, s.Model }
	defer func() {
		mu.Lock()
		if s.Model && spent > 0 {
			c.Speed = float64(output) / spent.Seconds()
		}
		mu.Unlock()
	}()
	sessions := []string{uuid.NewString(), uuid.NewString()}
	if s.Done != nil {
		defer func() {
			for _, id := range sessions {
				s.Done(id)
			}
		}()
	}
	skill := "---\nname: " + checkSkill + "\ndescription: The word of deck's self-check.\n---\n\nRun exactly this shell command once: echo " +
		skilled + " > " + filepath.Join(work, "skill.txt") + "\n"
	req := Request{Cwd: work, SessionID: sessions[0], MCPURL: url, PermissionMCP: s.PermissionMCP,
		Skills: map[string]string{checkSkill + "/SKILL.md": skill},
		OnEvent: func(e Event) {
			if e.Kind != EventStderr && e.Kind != EventRateLimit {
				Answered(ctx)
			}
		},
		OnUsage: func(u Usage) {
			Answered(ctx)
			mu.Lock()
			tokens += u.Input + u.Output + u.CacheRead
			output += u.Output
			spent += u.Duration
			mu.Unlock()
		},
		Prompt: "This is a self-check of deck. Do these steps in order, one at a time.\n1. Run exactly this shell command once: echo " + word + " > " + filepath.Join(work, "word.txt") +
			"\n2. Read your skill " + checkSkill + " and run the one shell command it gives." +
			"\n3. Run exactly this shell command once: sh " + filepath.Join(work, "probe.sh") +
			"\n4. Ask the owner with the ask tool of the deck MCP server: \"Which answer should the result carry?\" and wait for the answer." +
			"\n5. Only after you have the answer, hand in {\"word\": \"" + word + "\", \"answer\": <the answer you got>} with the result_submit tool of the deck MCP server, then end your turn."}
	type out struct{ Word, Answer string }
	// missed is set when a turn ended in words without result_submit, which a real turn is reminded of once.
	missed := false
	step := func(what string, r Request) (out, []string, int, bool) {
		missed = false
		mu.Lock()
		tokens = 0
		mu.Unlock()
		res, err := s.Run(ctx, r)
		c.Outcome = res.Held()
		raw, approved, asked := cs.Take()
		var o out
		_ = json.Unmarshal(raw, &o)
		mu.Lock()
		counted := tokens
		mu.Unlock()
		switch {
		case ctx.Err() != nil && res.Outcome == Login:
			c.Err = fmt.Errorf("%s: the model API refused the key (401 or 403); check the key secret and the url", what)
		case ctx.Err() != nil && res.Outcome == Limited:
			c.Err = fmt.Errorf("%s: the model API answered with a usage limit (429) until the ceiling", what)
		case ctx.Err() != nil && counted == 0:
			c.Slow = true
			model(fmt.Errorf("%s: not one model call finished within the ceiling: the model may be slow to start (a cold start, the queue of a free tier), or its url or model id may be wrong", what))
		case ctx.Err() != nil:
			c.Slow = true
			model(fmt.Errorf("%s: the model answered (%d tokens) but did not finish the steps within the ceiling: it is slow, such as a model that always reasons; commands with a long timeout_minutes may still run on it", what, counted))
		case err != nil:
			c.Err = fmt.Errorf("%s: %w", what, err)
		case strings.Contains(string(raw)+res.Text, secret):
			c.Err = fmt.Errorf("%s: the agent read a file deck hides", what)
		case res.Refusal != "" && raw == nil && strings.HasPrefix(res.Refusal, "404 "):
			c.Err = fmt.Errorf("%s: the model API does not know the model (%s): check the model id and the url of the connection", what, res.Refusal)
		case res.Refusal != "" && raw == nil && noTools(res.Refusal):
			c.Err = fmt.Errorf("%s: the model API refused a call with tools (%s): it may not take tool calls for this model; pick a model or a connection that does", what, res.Refusal)
		case res.Refusal != "" && raw == nil:
			c.Err = fmt.Errorf("%s: the model API refused what %s sent (%s): %s and this connection do not fit; pick another agent or connection for the model", what, s.Agent, res.Refusal, s.Agent)
		case res.Outcome != Done:
			c.Err = fmt.Errorf("%s: %s", what, res.Text)
		case raw == nil && asked == 0 && !wrote(work, word):
			model(fmt.Errorf("%s: the model made no tool call and answered in words: the API may not pass tools to it, or it does not use them (%s)", what, said(res.Text)))
		case raw == nil:
			missed = true
			model(fmt.Errorf("%s: the model ended its turn in words without calling result_submit (%s)", what, said(res.Text)))
		case counted == 0:
			c.Err = fmt.Errorf("%s: token counts: the turn reported no usage, so the model call journal would count zero", what)
		}
		return o, approved, asked, c.Err == nil
	}
	o, approved, asked, ok := step("first turn", req)
	if !ok && missed {
		c.Err, c.ModelFailed = nil, false
		remind := req
		remind.Resume = true
		remind.Prompt = "You ended your turn without handing in the result with the result_submit tool of the deck MCP server. " +
			"Do not redo the steps you did; do the ones left, then call result_submit."
		var more []string
		var n int
		o, more, n, ok = step("first turn, reminded of result_submit", remind)
		approved, asked = append(approved, more...), asked+n
	}
	if !ok {
		return c
	}
	written, _ := os.ReadFile(filepath.Join(work, "word.txt"))
	read, _ := os.ReadFile(filepath.Join(work, "skill.txt"))
	code, codeErr := os.ReadFile(filepath.Join(work, "probe.code"))
	probed, _ := os.ReadFile(filepath.Join(work, "probe.out"))
	switch {
	case strings.TrimSpace(string(written)) != word:
		model(fmt.Errorf("auto mode: the write in its own folder did not run (word.txt: %q)", string(written)))
	case strings.TrimSpace(string(read)) != skilled:
		model(fmt.Errorf("skills: the command of the skill deck gave the turn did not run: %s could not read the skill or did not follow it (skill.txt: %q)", s.Agent, string(read)))
	case codeErr != nil:
		model(errors.New("secrets: the probe that reads a hidden file did not run"))
	case strings.TrimSpace(string(code)) == "0" || strings.Contains(string(probed), secret):
		c.Err = fmt.Errorf("secrets: the shell of %s read a file deck hides", s.Agent)
	case strings.Contains(strings.Join(approved, "\n"), "word.txt"):
		c.Err = fmt.Errorf("auto mode: %s asked deck about a write in its own folder: %s", s.Agent, strings.Join(approved, "; "))
	case asked == 0 || o.Answer != answer:
		model(fmt.Errorf("question: the answer through the ask tool did not reach the result (%+v)", o))
	case o.Word != word:
		model(fmt.Errorf("result_submit: the result was %+v, want the word %s", o, word))
	}
	if c.Err != nil {
		return c
	}
	req.Resume = true
	req.Prompt = "deck did not keep the result of your last turn. Call the result_submit tool of the deck MCP server now with the same word and answer as before. Do not run any command."
	if o, _, _, ok = step("resume", req); !ok {
		return c
	}
	if o.Word != word || o.Answer != answer {
		model(fmt.Errorf("resume: the result was %+v, want the word %s and the answer %s", o, word, answer))
		return c
	}
	if sandbox.Contained() {
		return c
	}
	off := Request{Cwd: work, SessionID: sessions[1], MCPURL: req.MCPURL, PermissionMCP: req.PermissionMCP, NoNetwork: true, OnUsage: req.OnUsage,
		Prompt: "This is a self-check of deck. Run exactly this shell command once: sh " + filepath.Join(work, "net.sh") +
			"\nDo not ask for approval. Then hand in {\"word\": \"done\", \"answer\": \"\"} with the result_submit tool of the deck MCP server, even if the command failed."}
	if _, _, _, ok = step("turn without network", off); !ok {
		return c
	}
	switch got, err := os.ReadFile(filepath.Join(work, "net.out")); {
	case err != nil:
		model(fmt.Errorf("turn without network: the probe that runs curl did not run: %v", err))
	case strings.TrimSpace(string(got)) != "000":
		c.Err = fmt.Errorf("turn without network: curl in the sandbox of %s printed %q, want 000", s.Agent, string(got))
	}
	return c
}

// wrote reports whether the first step of the check wrote its word.
func wrote(work, word string) bool {
	got, _ := os.ReadFile(filepath.Join(work, "word.txt"))
	return strings.TrimSpace(string(got)) == word
}

// said is the start of what the model said, for the text of a failed check.
func said(text string) string {
	if strings.TrimSpace(text) == "" {
		return "it said nothing"
	}
	return "it said " + untrusted.Line(text, 200)
}
