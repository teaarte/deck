package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/provider"
)

// turn is one app-server session: it sets the thread up, runs one turn and collects what the turn reported.
type turn struct {
	a      *Adapter
	req    provider.Request
	c      *conn
	thread string
	turnID string
	model  string

	res       provider.Result
	completed int
	last      Turn
	final     string
	failure   *TurnError
	limits    RateLimitSnapshot

	gwMu sync.Mutex
	gw   *mcp.ClientSession
	// started and startedTurn are the thread and turn once the turn runs; approvals read them from their own
	// goroutines, with the file changes items announced.
	started, startedTurn string
	patches              map[string][]FileUpdateChange
}

func (t *turn) run(ctx context.Context, skills string) (provider.Result, error) {
	defer t.closeGateway()
	caps := &InitializeCapabilities{ExperimentalAPI: true, OptOutNotificationMethods: quiet}
	if err := t.call(ctx, "initialize", InitializeParams{ClientInfo: ClientInfo{Name: "deck", Title: "deck", Version: "0"}, Capabilities: caps}, nil); err != nil {
		return t.res, err
	}
	if err := t.c.notify("initialized", nil); err != nil {
		return t.res, err
	}
	var acct GetAccountResponse
	if err := t.call(ctx, "account/read", map[string]any{}, &acct); err != nil {
		return t.res, err
	}
	if len(acct.Account) == 0 || string(acct.Account) == "null" {
		return provider.Result{IsError: true, NeedsLogin: true, Text: "codex: not logged in; run codex login"}, nil
	}
	if skills != "" {
		if err := t.call(ctx, "skills/extraRoots/set", SkillsExtraRootsSetParams{ExtraRoots: []AbsolutePathBuf{AbsolutePathBuf(skills)}}, nil); err != nil {
			return t.res, err
		}
	}
	var cfg struct {
		Config struct {
			MCPServers map[string]json.RawMessage `json:"mcp_servers"`
		} `json:"config"`
	}
	if err := t.call(ctx, "config/read", ConfigReadParams{Cwd: t.req.Cwd}, &cfg); err != nil {
		return t.res, err
	}
	var servers []string
	for name := range cfg.Config.MCPServers {
		if name == server {
			return t.res, fmt.Errorf("codex: the owner's Codex config has an MCP server named %s, the name of the deck gateway; rename it there", server)
		}
		servers = append(servers, name)
	}
	slices.Sort(servers)
	var list SkillsListResponse
	if err := t.call(ctx, "skills/list", SkillsListParams{Cwds: []string{t.req.Cwd}, ForceReload: true}, &list); err != nil {
		return t.res, err
	}
	var skillsOff []string
	for _, e := range list.Data {
		for _, s := range e.Skills {
			if s.Enabled && !inside(string(s.Path), skills) {
				skillsOff = append(skillsOff, string(s.Path))
			}
		}
	}
	config := threadConfig(t.req, servers, skillsOff)
	policy := json.RawMessage(`"on-request"`)
	if t.thread == "" {
		var r ThreadStartResponse
		if err := t.call(ctx, "thread/start", ThreadStartParams{Cwd: t.req.Cwd, ApprovalPolicy: policy, Config: config}, &r); err != nil {
			return t.res, err
		}
		if r.Thread.ID == "" {
			return t.res, errors.New("codex started a thread without an id")
		}
		t.thread, t.model = r.Thread.ID, r.Model
		if err := t.a.remember(t.req.SessionID, t.thread); err != nil {
			return t.res, err
		}
	} else {
		var r ThreadResumeResponse
		err := t.call(ctx, "thread/resume", ThreadResumeParams{ThreadID: t.thread, Cwd: t.req.Cwd, ApprovalPolicy: policy, Config: config}, &r)
		if err != nil && strings.Contains(err.Error(), "no rollout found") {
			return provider.Result{SessionMissing: true}, nil
		}
		if err != nil {
			return t.res, err
		}
		if r.Thread.ID != t.thread {
			return t.res, fmt.Errorf("codex resumed thread %s instead of %s of deck session %s", r.Thread.ID, t.thread, t.req.SessionID)
		}
		t.model = r.Model
	}
	text, _ := json.Marshal(TextUserInput{Type: "text", Text: t.req.Prompt})
	var started TurnStartResponse
	if err := t.call(ctx, "turn/start", TurnStartParams{ThreadID: t.thread, Input: []UserInput{text}, Permissions: profile}, &started); err != nil {
		return t.res, fmt.Errorf("the prompt did not reach codex: %w", err)
	}
	t.turnID = started.Turn.ID
	t.gwMu.Lock()
	t.started, t.startedTurn = t.thread, t.turnID
	t.gwMu.Unlock()
	if t.turnID == "" {
		return t.res, errors.New("codex started a turn without an id")
	}
	if err := t.until(ctx); err != nil {
		return t.res, err
	}
	return t.finish()
}

// call sends a request and handles what arrives meanwhile, so the output never backs up.
func (t *turn) call(ctx context.Context, method string, params, out any) error {
	ch, err := t.c.start(method, params)
	if err != nil {
		return err
	}
	for {
		select {
		case m, ok := <-ch:
			return answer(m, ok, method, out)
		case m, ok := <-t.c.incoming:
			if !ok {
				return fmt.Errorf("codex app-server exited before answering %s", method)
			}
			t.handle(ctx, m)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// until reads the output until the turn completes; a stop of deck interrupts the turn first.
func (t *turn) until(ctx context.Context) error {
	cancelled := ctx.Done()
	var giveUp <-chan time.Time
	for t.completed == 0 {
		select {
		case m, ok := <-t.c.incoming:
			if !ok {
				return errors.New("codex app-server exited without finishing the turn")
			}
			t.handle(ctx, m)
		case <-cancelled:
			cancelled = nil
			_, _ = t.c.start("turn/interrupt", TurnInterruptParams{ThreadID: t.thread, TurnID: t.turnID})
			giveUp = time.After(15 * time.Second)
		case <-giveUp:
			return ctx.Err()
		}
	}
	// A second completion of the same turn would arrive right behind the first.
	grace := time.After(200 * time.Millisecond)
	for {
		select {
		case m, ok := <-t.c.incoming:
			if !ok {
				return nil
			}
			t.handle(ctx, m)
		case <-grace:
			return nil
		}
	}
}

func (t *turn) emit(kind, text string) {
	if t.req.OnEvent != nil {
		t.req.OnEvent(provider.Event{Kind: kind, Text: text})
	}
}

// handle takes one notification or server request; those of other threads, such as sub-agents, are skipped.
func (t *turn) handle(ctx context.Context, m message) {
	if len(m.ID) > 0 {
		go t.serve(ctx, m)
		return
	}
	var head struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(m.Params, &head)
	if m.Method == "account/rateLimits/updated" {
		var n AccountRateLimitsUpdatedNotification
		if decode(m.Params, &n) {
			t.limits = n.RateLimits
			if n.RateLimits.RateLimitReachedType != "" {
				t.emit("rate_limit", string(n.RateLimits.RateLimitReachedType))
			}
		}
		return
	}
	if head.ThreadID != t.thread || t.thread == "" {
		return
	}
	switch m.Method {
	case "turn/completed":
		var n TurnCompletedNotification
		if decode(m.Params, &n) && n.Turn.ID == t.turnID {
			t.completed++
			if t.completed == 1 {
				t.last = n.Turn
			}
		}
	case "item/started":
		var n ItemStartedNotification
		var it FileChangeThreadItem
		if decode(m.Params, &n) && n.TurnID == t.turnID && decode(n.Item, &it) && it.Type == "fileChange" {
			t.gwMu.Lock()
			if t.patches == nil {
				t.patches = map[string][]FileUpdateChange{}
			}
			t.patches[it.ID] = it.Changes
			t.gwMu.Unlock()
		}
	case "item/completed":
		var n ItemCompletedNotification
		if decode(m.Params, &n) && n.TurnID == t.turnID {
			t.item(n.Item)
		}
	case "thread/tokenUsage/updated":
		var n ThreadTokenUsageUpdatedNotification
		if decode(m.Params, &n) && n.TurnID == t.turnID && t.req.OnUsage != nil {
			u := n.TokenUsage.Last
			t.req.OnUsage(provider.Usage{Model: t.model, Input: u.InputTokens - u.CachedInputTokens, Output: u.OutputTokens,
				CacheRead: u.CachedInputTokens, CacheCreation: u.CacheWriteInputTokens})
		}
	case "error":
		var n ErrorNotification
		if decode(m.Params, &n) && n.TurnID == t.turnID && !n.WillRetry {
			t.failure = &n.Error
		}
	}
}

func (t *turn) item(raw ThreadItem) {
	var head struct{ Type string }
	_ = json.Unmarshal(raw, &head)
	switch head.Type {
	case "agentMessage":
		var it AgentMessageThreadItem
		if decode(raw, &it) {
			t.emit("text", it.Text)
			if string(it.Phase) == "final_answer" {
				t.final = it.Text
			}
		}
	case "commandExecution":
		var it CommandExecutionThreadItem
		if decode(raw, &it) {
			t.emit("tool", "shell")
			if it.Status == "declined" {
				t.res.Denied = append(t.res.Denied, "shell")
			}
		}
	case "mcpToolCall":
		var it MCPToolCallThreadItem
		if decode(raw, &it) {
			t.emit("tool", "mcp__"+it.Server+"__"+it.Tool)
		}
	case "webSearch":
		t.emit("tool", "web_search")
	case "fileChange":
		t.emit("tool", "apply_patch")
	}
}

// finish accepts the turn only if it completed exactly once, without an error, in the thread of the session.
func (t *turn) finish() (provider.Result, error) {
	res := t.res
	if t.completed > 1 {
		return res, fmt.Errorf("codex reported %d completions of one turn", t.completed)
	}
	res.Text = t.final
	switch t.last.Status {
	case "completed":
		return res, nil
	case "interrupted":
		res.IsError, res.Text = true, "codex: the turn was interrupted"
		return res, nil
	}
	res.IsError = true
	e := t.failure
	if t.last.Error != nil {
		e = t.last.Error
	}
	if e == nil {
		res.Text = "codex: the turn ended as " + string(t.last.Status)
		return res, nil
	}
	res.Text = e.Message
	var info string
	_ = json.Unmarshal(e.CodexErrorInfo, &info)
	switch {
	case info == "usageLimitExceeded":
		res.RateLimited, res.ResetsAt = true, t.resetsAt()
	case info == "unauthorized" || strings.Contains(string(e.CodexErrorInfo), `"httpStatusCode":401`) || strings.Contains(e.Message, "401 Unauthorized"):
		res.NeedsLogin = true
	}
	return res, nil
}

// resetsAt is when the window that ran out resets, in unix seconds; zero when no window says.
func (t *turn) resetsAt() int64 {
	var at int64
	for _, w := range []*RateLimitWindow{t.limits.Primary, t.limits.Secondary} {
		if w != nil && w.UsedPercent >= 100 && w.ResetsAt > at {
			at = w.ResetsAt
		}
	}
	return at
}

func inside(path, dir string) bool {
	if dir == "" {
		return false
	}
	for _, d := range paths(dir) {
		if strings.HasPrefix(path, d+"/") {
			return true
		}
	}
	return false
}
