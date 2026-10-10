package gateway

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Reach connects to the MCP server of deck at base as an agent turn does and checks that it lists the permission
// tool; the session it opens may call no command.
func (g *Gateway) Reach(ctx context.Context, base string) error {
	tok, err := g.Register(ctx, &Session{Commands: []string{}})
	if err != nil {
		return err
	}
	defer g.Unregister(tok)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	url := base + "/mcp/" + tok
	cl := mcp.NewClient(&mcp.Implementation{Name: "deck-reach", Version: "0"}, nil)
	cs, err := cl.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: url, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return fmt.Errorf("%s/mcp: %w", base, err)
	}
	defer cs.Close()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("%s/mcp: list tools: %w", base, err)
	}
	if !slices.ContainsFunc(res.Tools, func(t *mcp.Tool) bool { return t.Name == ApproveTool }) {
		return fmt.Errorf("%s/mcp lists no %s tool", base, ApproveTool)
	}
	return nil
}
