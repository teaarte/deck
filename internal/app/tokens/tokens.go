// Package tokens keeps the automation tokens: a terminal agent, Shortcuts or a script acting as the master with
// the commands its token lists.
package tokens

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"deck2/internal/nonce"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// TokenPrefix starts every automation token, so a token found in a log is recognised as deck's.
const TokenPrefix = "deck_"

var commandPattern = regexp.MustCompile(`^(\*|[a-z][a-z0-9-]*\.(\*|[a-z][a-z0-9_]*))$`)

// Service keeps the tokens; Journal writes a call to the journal of the gateway.
type Service struct {
	St      *store.Store
	MCPBase string
	Journal func(ctx context.Context, act registry.Actor, project int64, tool, class, outcome, params string)
}

func tokenHash(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// Automation resolves an automation token to the master-side actor it acts as; revoked and unknown tokens fail.
func (s *Service) Automation(ctx context.Context, raw string) (registry.Actor, error) {
	if !strings.HasPrefix(raw, TokenPrefix) {
		return registry.Actor{}, errors.New("not an automation token")
	}
	t, err := s.St.Q.FindTokenByHash(ctx, tokenHash(raw))
	if err != nil {
		return registry.Actor{}, errors.New("unknown or revoked automation token")
	}
	var cmds []string
	if err := json.Unmarshal([]byte(t.Commands), &cmds); err != nil {
		return registry.Actor{}, err
	}
	return registry.Actor{Kind: registry.Token, Workspace: t.WorkspaceID, Token: t.ID, Commands: cmds}, nil
}

type tokenCreateIn struct {
	Name     string   `desc:"What the token is for"`
	Commands []string `desc:"Command names it may call; project.* or deck.* allow every command under the prefix"`
}

func (s *Service) tokenCreate(ctx context.Context, act registry.Actor, in tokenCreateIn) (any, error) {
	if strings.TrimSpace(in.Name) == "" || len(in.Commands) == 0 {
		return nil, registry.Invalid("deck: a token needs a name and at least one command")
	}
	for _, c := range in.Commands {
		if !commandPattern.MatchString(c) {
			return nil, registry.Invalid("deck: %q is not a command name, project.* or *", c)
		}
	}
	raw := TokenPrefix + nonce.Hex(24)
	cmds, _ := json.Marshal(in.Commands)
	t, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (db.AutomationToken, error) {
		return q.CreateToken(ctx, db.CreateTokenParams{WorkspaceID: act.Workspace, Name: in.Name, Hash: tokenHash(raw), Commands: string(cmds)})
	})
	if err != nil {
		return nil, err
	}
	url := s.MCPBase + "/mcp"
	return map[string]any{
		"id": t.ID, "token": raw, "mcp_url": url,
		"claude_code": fmt.Sprintf("claude mcp add --transport http deck %s --header \"Authorization: Bearer %s\"", url, raw),
		"codex":       fmt.Sprintf("codex mcp add deck --url %s --bearer-token-env-var DECK_TOKEN, then run codex with DECK_TOKEN=%s", url, raw),
	}, nil
}

// tokenRow is an automation token with the commands it may call.
type tokenRow struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Commands  []string `json:"commands"`
	CreatedAt string   `json:"created_at"`
	Revoked   bool     `json:"revoked"`
}

type tokensListIn struct {
	Project string `type:"project" opt:"" desc:"Only the tokens that may call a command of this project"`
}

func (s *Service) tokensList(ctx context.Context, act registry.Actor, in tokensListIn) ([]tokenRow, error) {
	ts, err := s.St.Q.ListTokens(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]tokenRow, 0, len(ts))
	for _, t := range ts {
		r := tokenRow{ID: t.ID, Name: t.Name, Commands: []string{}, CreatedAt: t.CreatedAt, Revoked: t.RevokedAt.Valid}
		_ = json.Unmarshal([]byte(t.Commands), &r.Commands)
		if in.Project != "" && !slices.ContainsFunc(r.Commands, func(c string) bool { return c == "*" || strings.HasPrefix(c, in.Project+".") }) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

type tokenRevokeIn struct {
	Token int64 `desc:"Token id"`
}

func (s *Service) tokenRevoke(ctx context.Context, act registry.Actor, in tokenRevokeIn) (any, error) {
	n, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		return q.RevokeToken(ctx, db.RevokeTokenParams{ID: in.Token, WorkspaceID: act.Workspace})
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("deck: no active token %d", in.Token)
	}
	s.Journal(ctx, act, 0, registry.Builtin+".token_revoke", registry.External, "done", fmt.Sprintf(`{"token": %d}`, in.Token))
	return map[string]any{"revoked": in.Token}, nil
}

// Commands are the commands of tokens.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Def("token_create", "Create automation token",
			"Creates a token for a terminal agent, Shortcuts or a script; it acts as the master and may call only the listed commands.",
			registry.OwnerOnly, s.tokenCreate),
		registry.Read("tokens_list", "Automation tokens",
			"Lists automation tokens with their commands; revoked ones too.",
			registry.OwnerOnly, s.tokensList).On("tokens"),
		registry.Def("token_revoke", "Revoke token",
			"Revokes an automation token; calls with it are refused from then on.",
			registry.OwnerOnly, s.tokenRevoke).As(registry.External),
	}
}
