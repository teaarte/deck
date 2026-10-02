package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

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

// Check runs a short session as deck runs agents: a write in its folder, a hidden file its shell cannot read,
// ask, result_submit, token counts, a resume, then a turn whose curl has no network; deck reads the probes.
func (s ShellCheck) Check(ctx context.Context, start Check) (c Check) {
	c = start
	word, secret := CheckWord(), CheckWord()
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
	req := Request{Cwd: work, SessionID: sessions[0], MCPURL: url, PermissionMCP: s.PermissionMCP,
		OnUsage: func(u Usage) {
			mu.Lock()
			tokens += u.Input + u.Output + u.CacheRead
			output += u.Output
			spent += u.Duration
			mu.Unlock()
		},
		Prompt: "This is a self-check of deck. Do these steps in order, one at a time.\n1. Run exactly this shell command once: echo " + word + " > " + filepath.Join(work, "word.txt") +
			"\n2. Run exactly this shell command once: sh " + filepath.Join(work, "probe.sh") +
			"\n3. Ask the owner with the ask tool of the deck MCP server: \"Which answer should the result carry?\" and wait for the answer." +
			"\n4. Only after you have the answer, hand in {\"word\": \"" + word + "\", \"answer\": <the answer you got>} with the result_submit tool of the deck MCP server, then end your turn."}
	type out struct{ Word, Answer string }
	step := func(what string, r Request) (out, []string, int, bool) {
		mu.Lock()
		tokens = 0
		mu.Unlock()
		res, err := s.Run(ctx, r)
		c.RateLimited, c.NeedsLogin = res.RateLimited, res.NeedsLogin
		raw, approved, asked := cs.Take()
		var o out
		_ = json.Unmarshal(raw, &o)
		mu.Lock()
		counted := tokens
		mu.Unlock()
		switch {
		case err != nil && ctx.Err() != nil:
			model(fmt.Errorf("%s: the model did not finish within the ceiling: %w", what, err))
		case err != nil:
			c.Err = fmt.Errorf("%s: %w", what, err)
		case res.RateLimited || res.NeedsLogin || res.IsError:
			c.Err = fmt.Errorf("%s: %s", what, res.Text)
		case strings.Contains(string(raw)+res.Text, secret):
			c.Err = fmt.Errorf("%s: the agent read a file deck hides", what)
		case raw == nil:
			model(fmt.Errorf("%s: no result came in with result_submit", what))
		case counted == 0:
			c.Err = fmt.Errorf("%s: token counts: the turn reported no usage, so the model call journal would count zero", what)
		}
		return o, approved, asked, c.Err == nil
	}
	o, approved, asked, ok := step("first turn", req)
	if !ok {
		return c
	}
	written, _ := os.ReadFile(filepath.Join(work, "word.txt"))
	code, codeErr := os.ReadFile(filepath.Join(work, "probe.code"))
	read, _ := os.ReadFile(filepath.Join(work, "probe.out"))
	switch {
	case strings.TrimSpace(string(written)) != word:
		model(fmt.Errorf("auto mode: the write in its own folder did not run (word.txt: %q)", string(written)))
	case codeErr != nil:
		model(errors.New("secrets: the probe that reads a hidden file did not run"))
	case strings.TrimSpace(string(code)) == "0" || strings.Contains(string(read), secret):
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
	off := Request{Cwd: work, SessionID: sessions[1], MCPURL: req.MCPURL, PermissionMCP: req.PermissionMCP, NoNetwork: true, OnUsage: req.OnUsage,
		Prompt: "This is a self-check of deck. Run exactly this shell command once: sh " + filepath.Join(work, "net.sh") +
			"\nDo not ask for approval. Then hand in {\"word\": \"done\", \"answer\": \"\"} with the result_submit tool of the deck MCP server, even if the command failed."}
	if _, _, _, ok = step("no network", off); !ok {
		return c
	}
	switch got, err := os.ReadFile(filepath.Join(work, "net.out")); {
	case err != nil:
		model(fmt.Errorf("no network: the probe that runs curl did not run: %v", err))
	case strings.TrimSpace(string(got)) != "000":
		c.Err = fmt.Errorf("no network: curl in the sandbox of %s printed %q, want 000", s.Agent, string(got))
	}
	return c
}
