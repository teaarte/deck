package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"deck2/internal/nonce"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// TokenPrefix starts every automation token, so a token found in a log is recognised as deck's.
const TokenPrefix = "deck_"

var commandPattern = regexp.MustCompile(`^(\*|[a-z][a-z0-9-]*\.(\*|[a-z][a-z0-9_]*))$`)

func tokenHash(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// Automation resolves an automation token to the master-side actor it acts as; revoked and unknown tokens fail.
func (a *App) Automation(ctx context.Context, raw string) (registry.Actor, error) {
	if !strings.HasPrefix(raw, TokenPrefix) {
		return registry.Actor{}, errors.New("not an automation token")
	}
	t, err := a.Store.Q.FindTokenByHash(ctx, tokenHash(raw))
	if err != nil {
		return registry.Actor{}, errors.New("unknown or revoked automation token")
	}
	var cmds []string
	if err := json.Unmarshal([]byte(t.Commands), &cmds); err != nil {
		return registry.Actor{}, err
	}
	return registry.Actor{Kind: registry.Master, Workspace: t.WorkspaceID, Token: t.ID, Commands: cmds}, nil
}

type tokenCreateIn struct {
	Name     string   `desc:"What the token is for"`
	Commands []string `desc:"Command names it may call; project.* or deck.* allow every command under the prefix"`
}

func (a *App) tokenCreate(ctx context.Context, act registry.Actor, in tokenCreateIn) (any, error) {
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
	t, err := store.TxGet(a.Store, ctx, func(ctx context.Context, q *db.Queries) (db.AutomationToken, error) {
		return q.CreateToken(ctx, db.CreateTokenParams{WorkspaceID: act.Workspace, Name: in.Name, Hash: tokenHash(raw), Commands: string(cmds)})
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("tokens")
	url := a.Cfg.MCPBase + "/mcp"
	return map[string]any{
		"id": t.ID, "token": raw, "mcp_url": url,
		"claude_code": fmt.Sprintf("claude mcp add --transport http deck %s --header \"Authorization: Bearer %s\"", url, raw),
		"codex":       fmt.Sprintf("codex mcp add deck --url %s --bearer-token-env-var DECK_TOKEN, then run codex with DECK_TOKEN=%s", url, raw),
	}, nil
}

func (a *App) tokensList(ctx context.Context, act registry.Actor, _ struct{}) (any, error) {
	ts, err := a.Store.Q.ListTokens(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		out = append(out, map[string]any{"id": t.ID, "name": t.Name, "commands": json.RawMessage(t.Commands),
			"created_at": t.CreatedAt, "revoked": t.RevokedAt.Valid})
	}
	return out, nil
}

type tokenRevokeIn struct {
	Token int64 `desc:"Token id"`
}

func (a *App) tokenRevoke(ctx context.Context, act registry.Actor, in tokenRevokeIn) (any, error) {
	n, err := store.TxGet(a.Store, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		return q.RevokeToken(ctx, db.RevokeTokenParams{ID: in.Token, WorkspaceID: act.Workspace})
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("deck: no active token %d", in.Token)
	}
	a.Gateway.Journal(ctx, act, 0, registry.Builtin+".token_revoke", registry.External, "done", fmt.Sprintf(`{"token": %d}`, in.Token))
	a.Bus.Publish("tokens")
	return map[string]any{"revoked": in.Token}, nil
}

func (a *App) tokensCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("token_create", "Create automation token",
			"Creates a token for a terminal agent, Shortcuts or a script; it acts as the master and may call only the listed commands.",
			registry.OwnerOnly, a.tokenCreate),
		registry.Def("tokens_list", "Automation tokens",
			"Lists automation tokens with their commands; revoked ones too.",
			registry.OwnerOnly, a.tokensList),
		registry.Def("token_revoke", "Revoke token",
			"Revokes an automation token; calls with it are refused from then on.",
			registry.OwnerOnly, a.tokenRevoke).As(registry.External),
	}
}
