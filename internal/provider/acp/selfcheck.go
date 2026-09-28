package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"deck2/internal/provider"
	"deck2/internal/sandbox"
)

var semver = regexp.MustCompile(`\d+\.\d+\.\d+[0-9A-Za-z.+-]*`)

// Version is the version the agent reports, such as 1.18.20; the agent runs in the sandbox of deck for it too.
func (a *Adapter) Version(ctx context.Context) (string, error) {
	bin, err := exec.LookPath(a.cfg.Binary)
	if err != nil {
		return "", fmt.Errorf("acp: %s is not installed: %w", a.cfg.Agent, err)
	}
	if err := os.MkdirAll(a.Dir, 0o700); err != nil {
		return "", err
	}
	home, err := os.MkdirTemp(a.Dir, "version-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(home)
	cmd, err := a.cfg.Sandbox.Command(ctx, sandbox.Policy{Argv: []string{bin, "--version"}, Dir: home,
		Env: append(environ(os.Environ()), "HOME="+home, "TMPDIR="+home), Write: []string{home},
		Hide: sandbox.Hidden(a.cfg.Home)})
	if err != nil {
		return "", err
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", a.cfg.Agent, err)
	}
	v := semver.FindString(string(out))
	if v == "" {
		return "", fmt.Errorf("%s --version printed no version: %q", a.cfg.Agent, untrustedLine(out))
	}
	return a.cfg.Agent + " " + v, nil
}

func untrustedLine(b []byte) string {
	s, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// SelfCheck runs a short session as deck runs agents: a write in its folder, a hidden file its shell cannot read,
// ask, result_submit, token counts, a resume, then a turn whose curl has no network. deck reads the probes itself.
func (a *Adapter) SelfCheck(ctx context.Context) (c provider.Check) {
	if c.Version, c.Err = a.Version(ctx); c.Err != nil {
		return c
	}
	word, secret := provider.CheckWord(), provider.CheckWord()
	answer := word + "-answered"
	cs := &provider.CheckServer{Answer: answer}
	url, stop, err := provider.ServeCheck(cs.Server())
	if err != nil {
		c.Err = err
		return c
	}
	defer stop()
	if err := os.MkdirAll(a.Dir, 0o700); err != nil {
		c.Err = err
		return c
	}
	dir, err := os.MkdirTemp(a.Dir, "selfcheck-")
	if err != nil {
		c.Err = err
		return c
	}
	defer os.RemoveAll(dir)
	work, hidden := filepath.Join(dir, "work"), filepath.Join(dir, "secrets")
	key := filepath.Join(hidden, "key.txt")
	probe := "cd '" + work + "'; cat '" + key + "' > probe.out 2>&1; echo $? > probe.code\n"
	netProbe := "cd '" + work + "'; curl -s -m 5 -o /dev/null -w '%{http_code}' https://example.com > net.out 2>/dev/null\n"
	if err := errors.Join(os.Mkdir(work, 0o700), os.Mkdir(hidden, 0o700), os.WriteFile(key, []byte(secret+"\n"), 0o600),
		os.WriteFile(filepath.Join(work, "probe.sh"), []byte(probe), 0o600), os.WriteFile(filepath.Join(work, "net.sh"), []byte(netProbe), 0o600)); err != nil {
		c.Err = err
		return c
	}
	var tokens, output int64
	var spent time.Duration
	var mu sync.Mutex
	model := func(err error) { c.Err, c.ModelFailed = err, true }
	defer func() {
		mu.Lock()
		if spent > 0 {
			c.Speed = float64(output) / spent.Seconds()
		}
		mu.Unlock()
	}()
	sessions := []string{uuid.NewString(), uuid.NewString()}
	defer func() {
		for _, s := range sessions {
			_ = os.RemoveAll(a.sessionDir(s))
		}
	}()
	req := provider.Request{Cwd: work, SessionID: sessions[0], MCPURL: url,
		OnUsage: func(u provider.Usage) {
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
	step := func(what string, r provider.Request) (out, int, bool) {
		mu.Lock()
		tokens = 0
		mu.Unlock()
		res, err := a.Run(ctx, r)
		c.NeedsLogin = res.NeedsLogin
		raw, _, asked := cs.Take()
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
		case res.NeedsLogin || res.IsError:
			c.Err = fmt.Errorf("%s: %s", what, res.Text)
		case strings.Contains(string(raw)+res.Text, secret):
			c.Err = fmt.Errorf("%s: the agent read a file deck hides", what)
		case raw == nil:
			model(fmt.Errorf("%s: no result came in with result_submit", what))
		case counted == 0:
			c.Err = fmt.Errorf("%s: token counts: the model API reported no usage through deck's proxy", what)
		}
		return o, asked, c.Err == nil
	}
	o, asked, ok := step("first turn", req)
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
		c.Err = fmt.Errorf("secrets: the shell of %s read a file deck hides", a.cfg.Agent)
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
	if o, _, ok = step("resume", req); !ok {
		return c
	}
	if o.Word != word || o.Answer != answer {
		model(fmt.Errorf("resume: the result was %+v, want the word %s and the answer %s", o, word, answer))
		return c
	}
	off := provider.Request{Cwd: work, SessionID: sessions[1], MCPURL: req.MCPURL, NoNetwork: true, OnUsage: req.OnUsage,
		Prompt: "This is a self-check of deck. Run exactly this shell command once: sh " + filepath.Join(work, "net.sh") +
			"\nThen hand in {\"word\": \"done\", \"answer\": \"\"} with the result_submit tool of the deck MCP server, even if the command failed."}
	if _, _, ok = step("no network", off); !ok {
		return c
	}
	switch got, err := os.ReadFile(filepath.Join(work, "net.out")); {
	case err != nil:
		model(fmt.Errorf("no network: the probe that runs curl did not run: %v", err))
	case strings.TrimSpace(string(got)) != "000":
		c.Err = fmt.Errorf("no network: curl in the sandbox of deck printed %q, want 000", string(got))
	}
	return c
}
