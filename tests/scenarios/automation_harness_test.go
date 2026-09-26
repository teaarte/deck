package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// bearer adds an automation token to every request, the way a terminal agent's MCP config does.
type bearer string

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

// token creates an automation token as the owner and returns its id and text.
func (e *Env) token(name string, commands ...string) (int64, string) {
	e.T.Helper()
	var t struct {
		ID    int64
		Token string
	}
	e.must("deck.token_create", map[string]any{"name": name, "commands": commands}, &t)
	return t.ID, t.Token
}

// terminal connects to the MCP entry of automation tokens like `claude mcp add --transport http` does.
func (e *Env) terminal(tok string) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "terminal", Version: "0"}, nil)
	s, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: e.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearer(tok)}}, nil)
	if err == nil {
		e.T.Cleanup(func() { _ = s.Close() })
	}
	return s, err
}

// use calls a tool of a terminal session and returns its text and whether it failed.
func use(s *mcp.ClientSession, name string, args any) (string, bool) {
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

func toolNames(s *mcp.ClientSession) []string {
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

type journalRow struct {
	Actor, Tool, Class, Outcome string
}

func (e *Env) journal() []journalRow {
	e.T.Helper()
	rows, err := e.App.Store.DB.Query(`SELECT actor, tool, class, outcome FROM tool_call ORDER BY id`)
	if err != nil {
		e.T.Fatal(err)
	}
	defer rows.Close()
	var out []journalRow
	for rows.Next() {
		var r journalRow
		_ = rows.Scan(&r.Actor, &r.Tool, &r.Class, &r.Outcome)
		out = append(out, r)
	}
	return out
}

func (e *Env) doWith(tok, method, path string) (int, []byte) {
	e.T.Helper()
	req, _ := http.NewRequest(method, e.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (e *Env) tokenRows() []string {
	e.T.Helper()
	rows, err := e.App.Store.DB.Query(`SELECT name || ' ' || hash || ' ' || commands FROM automation_token`)
	if err != nil {
		e.T.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out = append(out, s)
	}
	return out
}
