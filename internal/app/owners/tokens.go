// Package owners keeps who may call deck as its owner: the devices of the owner with their pushes and pairing, the
// automation tokens a terminal agent, Shortcuts or a script acts with, and the tokens deck lends its own browser.
package owners

import (
	"context"
	"crypto/sha256"
	"deck2/internal/events"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"deck2/internal/auth"
	"deck2/internal/manifest"
	"deck2/internal/node"
	"deck2/internal/nonce"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// TokenPrefix starts every automation token, so a token found in a log is recognised as deck's.
const TokenPrefix = "deck_"

// deckCommand is a command of deck a token names, or every one of them; moduleSubject a module, every module or one
// command of a module, the subject of a right of a token.
var (
	deckCommand   = regexp.MustCompile(`^deck\.(\*|[a-z][a-z0-9_]*)$`)
	moduleSubject = regexp.MustCompile(`^(\*|[a-z][a-z0-9-]*(\.[a-z][a-z0-9_]*)?)$`)
)

// Service keeps the devices and tokens; Journal writes a call to the journal of the gateway.
type Service struct {
	St      *store.Store
	Now     func() time.Time
	MCPBase string
	loans   loans
	Journal func(ctx context.Context, act registry.Actor, project int64, tool, class, outcome, params, errText string)
	// Code makes a code that pairs a device; Kick ends the open connections of a revoked device; Origins are the
	// addresses of deck.
	Code    func(workspace int64, kind, name string) (string, time.Time, error)
	Kick    func(device int64)
	Origins func() []auth.Origin
	// PushNew pushes what came since the last push of a workspace.
	PushNew func(ctx context.Context, workspace int64)
	// Node is the state of the connection of a node; Probe asks a node which addresses of its machine answer and what
	// of its folders deck may use.
	Node  func(device int64) node.State
	Probe func(ctx context.Context, device int64, addresses, paths []string) (node.Probed, error)
	Go    func(func(ctx context.Context)) bool
}

func tokenHash(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// Automation resolves an automation token to the actor it acts as, inside the modules its rights reach; revoked and
// unknown tokens fail.
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
	ps, err := s.St.Q.ListWorkspaceProjects(ctx, t.WorkspaceID)
	if err != nil {
		return registry.Actor{}, err
	}
	act := registry.Actor{Kind: registry.Token, Workspace: t.WorkspaceID, Token: t.ID}
	act.Commands, act.Scope = reach(rightsOf(t.Rights), ps, cmds)
	return act, nil
}

// rightsOf are the rights of a token as it keeps them.
func rightsOf(raw string) []manifest.Declared {
	var out []manifest.Declared
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// reach is what a token's rights reach among ps: the commands it calls, deck's beside its modules', and the projects it
// reads and acts in; a module whole gives both, one of its commands that command alone, * every module but the master.
func reach(rights []manifest.Declared, ps []db.Project, deck []string) ([]string, registry.Scope) {
	calls := slices.Clone(deck)
	var reads, acts []int64
	every := false
	for _, r := range rights {
		for _, subject := range r.What {
			every = every || subject == "*" && r.Right == manifest.Modules
			for _, p := range ps {
				whole := subject == p.Name || subject == "*" && p.Builtin == 0
				switch {
				case whole && r.Right == manifest.Modules:
					calls, acts, reads = append(calls, p.Name+".*"), append(acts, p.ID), append(reads, p.ID)
				case whole && r.Right == manifest.Data:
					reads = append(reads, p.ID)
				case r.Right == manifest.Modules && strings.HasPrefix(subject, p.Name+"."):
					calls = append(calls, subject)
				}
			}
		}
	}
	slices.Sort(reads)
	slices.Sort(acts)
	return calls, registry.Scope{Reads: slices.Compact(reads), Acts: slices.Compact(acts), Every: every}
}

type tokenCreateIn struct {
	Name     string   `desc:"What the token is for"`
	Commands []string `opt:"" desc:"Commands of deck it may call, as deck.records_list; deck.* every one"`
	Data     []string `opt:"" desc:"Modules whose data it reads through the commands of deck; * every module but the master"`
	Modules  []string `opt:"" desc:"Modules whose commands it calls and where the commands of deck act for it, or one command of a module, as notes.add; * every module but the master"`
}

func (s *Service) tokenCreate(ctx context.Context, act registry.Actor, in tokenCreateIn) (any, error) {
	if strings.TrimSpace(in.Name) == "" || len(in.Commands)+len(in.Data)+len(in.Modules) == 0 {
		return nil, registry.Invalid("a token needs a name and at least one command of deck or one module")
	}
	for _, c := range in.Commands {
		if !deckCommand.MatchString(c) {
			return nil, registry.Invalid("%q is not a command of deck or deck.*; the commands of a module come with modules", c)
		}
	}
	var rights []manifest.Declared
	for _, r := range []manifest.Declared{{Right: manifest.Data, What: in.Data}, {Right: manifest.Modules, What: in.Modules}} {
		for _, subject := range r.What {
			if !moduleSubject.MatchString(subject) || r.Right == manifest.Data && strings.Contains(subject, ".") {
				return nil, registry.Invalid("%q is not a module or *, or of modules a command of a module", subject)
			}
		}
		if len(r.What) > 0 {
			rights = append(rights, r)
		}
	}
	raw := TokenPrefix + nonce.Hex(24)
	cmds, _ := json.Marshal(append([]string{}, in.Commands...))
	rs, _ := json.Marshal(append([]manifest.Declared{}, rights...))
	t, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (db.AutomationToken, error) {
		return q.CreateToken(ctx, db.CreateTokenParams{WorkspaceID: act.Workspace, Name: in.Name, Hash: tokenHash(raw), Commands: string(cmds), Rights: string(rs)})
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

// tokenView is an automation token with the commands of deck it calls and the modules its rights reach: Data those
// whose data it reads, Modules those or the commands of them it calls.
type tokenView struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Commands  []string `json:"commands"`
	Data      []string `json:"data"`
	Modules   []string `json:"modules"`
	CreatedAt string   `json:"created_at"`
	Revoked   bool     `json:"revoked"`
	// Trusted says the owner vouches for what it writes; else it is text from outside.
	Trusted bool `json:"trusted"`
}

type tokensListIn struct {
	Project string `type:"project" opt:"" desc:"Only the tokens whose rights reach this project"`
}

func (s *Service) tokensList(ctx context.Context, act registry.Actor, in tokensListIn) ([]tokenView, error) {
	ts, err := s.St.Q.ListTokens(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	reaches := func(subject string) bool {
		return subject == "*" || subject == in.Project || strings.HasPrefix(subject, in.Project+".")
	}
	out := make([]tokenView, 0, len(ts))
	for _, t := range ts {
		r := tokenView{ID: t.ID, Name: t.Name, Commands: []string{}, Data: []string{}, Modules: []string{}, CreatedAt: t.CreatedAt,
			Revoked: t.RevokedAt.Valid, Trusted: t.Trusted == 1}
		_ = json.Unmarshal([]byte(t.Commands), &r.Commands)
		for _, d := range rightsOf(t.Rights) {
			switch d.Right {
			case manifest.Data:
				r.Data = append(r.Data, d.What...)
			case manifest.Modules:
				r.Modules = append(r.Modules, d.What...)
			}
		}
		if in.Project != "" && !slices.ContainsFunc(r.Data, reaches) && !slices.ContainsFunc(r.Modules, reaches) {
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
		return q.RevokeToken(ctx, db.RevokeTokenParams{ID: in.Token, WorkspaceID: act.Workspace, Now: store.Time(s.Now())})
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("no active token %d", in.Token)
	}
	s.Journal(ctx, act, 0, registry.Deck("token_revoke"), "", "done", fmt.Sprintf(`{"token": %d}`, in.Token), "")
	return map[string]any{"revoked": in.Token}, nil
}

// Commands are the commands of devices, pushes and tokens.
func (s *Service) Commands() []*registry.Command {
	return slices.Concat(s.deviceCommands(), s.pushCommands(), s.tokenCommands())
}

func (s *Service) tokenCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("token_create", "Create automation token",
			"Creates a token for a terminal agent, Shortcuts or a script, with the owner's fresh passkey: it calls the commands of deck it lists, reads the data of the modules data names, and calls the commands of the modules modules names, where deck's commands act for it too; no other module.",
			registry.OwnerOnly, s.tokenCreate).Stays().Fresh(nil),
		registry.Read("tokens_list", "Automation tokens",
			"Lists automation tokens by name with the commands of deck they call and the modules their rights reach, never the tokens themselves; revoked ones too.",
			registry.OwnerSide, s.tokensList).Brings(registry.FromDeck).On(events.Devices).NoTokens(),
		registry.Def("token_revoke", "Revoke token",
			"Revokes an automation token; calls with it are refused from then on.",
			registry.OwnerOnly, s.tokenRevoke).Stays(),
		registry.Def("token_trust", "Trust what a token writes",
			"Says whether the owner vouches for what an automation token writes into deck: by default it is text from outside, which marks the turns that read it; a trusted token's words are the owner's.",
			registry.OwnerOnly, s.tokenTrust).Stays().Fresh(trusts),
	}
}

type tokenTrustIn struct {
	Token   int64 `desc:"Token id"`
	Trusted bool  `desc:"Whether the owner vouches for what it writes"`
}

// trusts says a call of token_trust vouches for a token, which takes the owner's fresh passkey; taking it back does not.
func trusts(_ context.Context, _ registry.Actor, input json.RawMessage) (bool, error) {
	var in tokenTrustIn
	return json.Unmarshal(input, &in) != nil || in.Trusted, nil
}

// tokenTrust keeps whether the owner vouches for what a token writes, which only the owner says.
func (s *Service) tokenTrust(ctx context.Context, act registry.Actor, in tokenTrustIn) (any, error) {
	n, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		return q.SetTokenTrusted(ctx, db.SetTokenTrustedParams{Trusted: store.Bool(in.Trusted), ID: in.Token, WorkspaceID: act.Workspace})
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("no active token %d", in.Token)
	}
	s.Journal(ctx, act, 0, registry.Deck("token_trust"), "", "done", fmt.Sprintf(`{"token": %d, "trusted": %t}`, in.Token, in.Trusted), "")
	return map[string]any{"token": in.Token, "trusted": in.Trusted}, nil
}
