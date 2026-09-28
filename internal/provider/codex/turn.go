package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

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

func inside(path, dir string) bool {
	if dir == "" {
		return false
	}
	for _, d := range provider.Paths(dir) {
		if strings.HasPrefix(path, d+"/") {
			return true
		}
	}
	return false
}
