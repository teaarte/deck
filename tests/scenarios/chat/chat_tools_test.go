package chat

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestTheToolsOfTheMastersTurnStayCompact: the tools a reply of the master lists, the shapes of answers in their
// descriptions too, stay within what a provider takes and a budget every turn pays for.
func TestTheToolsOfTheMastersTurnStayCompact(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var tools []*mcp.Tool
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		res, err := c.Session.ListTools(ctx, &mcp.ListToolsParams{})
		if err == nil {
			mu.Lock()
			tools = res.Tools
			mu.Unlock()
		}
		return provider.Result{Text: "ok"}
	}))
	e.MasterSaid(e.Say(0, "hello"), 1)
	mu.Lock()
	defer mu.Unlock()
	raw, _ := json.Marshal(tools)
	desc := 0
	for _, tl := range tools {
		desc += len(tl.Description)
	}
	t.Logf("master's turn: %d tools, %d bytes of tools/list (~%d tokens), %d bytes of descriptions", len(tools), len(raw), len(raw)/4, desc)
	if len(tools) == 0 || len(tools) > 128 || len(raw) > toolsBudget {
		t.Fatalf("the tools of the master's turn: %d tools of at most 128, %d bytes of at most %d", len(tools), len(raw), toolsBudget)
	}
}

// toolsBudget is the bytes of tools/list of the master's turn without modules: 73 KB when it was set, a few more allowed.
const toolsBudget = 80 << 10
