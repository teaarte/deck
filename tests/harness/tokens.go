package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/gateway"
	"deck2/internal/registry"
)

// bearer adds an automation token to every request, the way a terminal agent's MCP config does.
type bearer string

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

// Token creates an automation token of names of commands as tokens were before their rights, as migration 86 moves
// them: module names go to modules, whole with a command of deck, every module when it names none; its id and text.
func (e *Env) Token(name string, commands ...string) (int64, string) {
	e.T.Helper()
	deck, modules := []string{}, []string{}
	for _, c := range commands {
		switch {
		case c == "*":
			deck = append(deck, "deck.*")
		case strings.HasPrefix(c, "deck."):
			deck = append(deck, c)
		}
	}
	for _, c := range commands {
		module, command, _ := strings.Cut(c, ".")
		switch {
		case c == "*" || module == "deck":
		case len(deck) > 0 || command == "*":
			modules = append(modules, module)
		default:
			modules = append(modules, c)
		}
	}
	if len(deck) > 0 && len(modules) == 0 {
		modules = []string{"*"}
	}
	return e.TokenWith(map[string]any{"name": name, "commands": deck, "modules": slices.Compact(modules)})
}

// TokenWith creates an automation token of an input of deck.token_create and returns its id and text.
func (e *Env) TokenWith(in map[string]any) (int64, string) {
	e.T.Helper()
	var t struct {
		ID    int64
		Token string
	}
	e.Must("deck.token_create", in, &t)
	return t.ID, t.Token
}

// Terminal connects to the MCP entry of automation tokens like `claude mcp add --transport http` does.
func (e *Env) Terminal(tok string) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "terminal", Version: "0"}, nil)
	s, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: e.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearer(tok)}}, nil)
	if err == nil {
		e.T.Cleanup(func() { _ = s.Close() })
	}
	return s, err
}

// Use calls a tool of a terminal session and returns its text and whether it failed.
func Use(s *mcp.ClientSession, name string, args any) (string, bool) {
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err.Error(), true
	}
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String(), res.IsError
}

func ToolNames(s *mcp.ClientSession) []string {
	var out []string
	for t, err := range s.Tools(context.Background(), nil) {
		if err != nil {
			return append(out, "error: "+err.Error())
		}
		out = append(out, t.Name)
	}
	return out
}

// callWith invokes a command over the HTTP API with the given bearer token.
func (e *Env) callWith(tok, name string, input any) (int, []byte) {
	e.T.Helper()
	b, _ := json.Marshal(input)
	req, _ := http.NewRequest("POST", e.URL+"/api/commands/"+name, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

type JournalRow struct {
	Actor, Tool, Need, Outcome string
}

func (e *Env) Journal() []JournalRow {
	e.T.Helper()
	rows := e.SQLRows(`SELECT actor, tool, need, outcome FROM tool_call ORDER BY id`)
	defer rows.Close()
	var out []JournalRow
	for rows.Next() {
		var r JournalRow
		_ = rows.Scan(&r.Actor, &r.Tool, &r.Need, &r.Outcome)
		out = append(out, r)
	}
	return out
}

func (e *Env) TokenRows() []string {
	e.T.Helper()
	rows := e.SQLRows(`SELECT name || ' ' || hash || ' ' || commands FROM automation_token`)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

// Sends makes a command of a test one that needs a right: send on itself, as a command of deck a module proposes.
func Sends(c *registry.Command) *registry.Command {
	c.Gate.Needs = gateway.Itself(c.Name, false)
	return c.Brings(registry.FromDeck)
}
