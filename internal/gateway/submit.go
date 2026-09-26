package gateway

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const SubmitTool = "result_submit"

// Submit is how a session whose provider has no output by schema hands in its result; deck checks it.
type Submit struct {
	Schema   json.RawMessage
	Validate func(json.RawMessage) error
	// Retried is set once the one request to rebuild the result is spent, in this turn or an earlier one.
	Retried bool

	mu   sync.Mutex
	got  json.RawMessage
	done bool
}

// Result is what the session handed in, whether it handed anything in, and whether the rebuild is spent.
func (s *Submit) Result() (raw json.RawMessage, handed, retried bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.got, s.done, s.Retried
}

// Spend uses up the one request to rebuild the result.
func (s *Submit) Spend() {
	s.mu.Lock()
	s.Retried = true
	s.mu.Unlock()
}

func (s *Submit) tool() *mcp.Tool {
	return &mcp.Tool{Name: SubmitTool, Title: "Hand in the result",
		Description: "Hands in the final result of the task by its schema, once; null means there was nothing to do.",
		InputSchema: map[string]any{"type": "object", "required": []string{"result"}, "properties": map[string]any{
			"result": map[string]any{"anyOf": []any{s.Schema, map[string]any{"type": "null"}}}}}}
}

func (s *Submit) handle(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return refusal("deck: the result was already handed in; end your turn."), nil
	}
	v := req.Params.Arguments
	var in map[string]json.RawMessage
	if json.Unmarshal(v, &in) == nil {
		if r, ok := in["result"]; ok {
			v = r
		}
	}
	if err := s.Validate(v); err != nil {
		if !s.Retried {
			s.Retried = true
			return refusal("deck: the result does not match its schema: " + err.Error() +
				"\nDo not redo the work. Call result_submit again with only the corrected JSON."), nil
		}
		s.got, s.done = append(json.RawMessage{}, v...), true
		return refusal("deck: the result still does not match its schema; deck keeps it marked as not by the schema. End your turn."), nil
	}
	s.got, s.done = append(json.RawMessage{}, v...), true
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "deck: result accepted. End your turn."}}}, nil
}

func refusal(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}
