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
	// Retried is set once the one request to rebuild the result is spent in this run of the session.
	Retried bool
	// Accepted keeps a result by the schema before the agent hears it was accepted.
	Accepted func(json.RawMessage)

	mu sync.Mutex
	// got is the last result handed in, including one the schema refused; taken says the schema took it.
	got         json.RawMessage
	done, taken bool
}

// Kept is the result a process already handed in before this run of its session, which a second one does not replace.
func (s *Submit) Kept(raw json.RawMessage) {
	s.mu.Lock()
	s.got, s.done, s.taken = raw, true, true
	s.mu.Unlock()
}

// Taken says the session handed in a result its schema took.
func (s *Submit) Taken() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.taken
}

// Result is the last result the session handed in, and whether the rebuild is spent.
func (s *Submit) Result() (raw json.RawMessage, retried bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.got, s.Retried
}

// Spend uses up the one request to rebuild the result.
func (s *Submit) Spend() {
	s.mu.Lock()
	s.Retried = true
	s.mu.Unlock()
}

func (s *Submit) tool() *mcp.Tool {
	return &mcp.Tool{Name: SubmitTool, Title: "Hand in the result",
		Description: "Hands in the final result of the task by its schema, once; where the schema takes null, null means there was nothing to do.",
		InputSchema: map[string]any{"type": "object", "required": []string{"result"}, "properties": map[string]any{
			"result": s.Schema}}}
}

func (s *Submit) handle(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return refusal("deck: the result was already handed in; end your turn."), nil
	}
	var in map[string]json.RawMessage
	_ = json.Unmarshal(req.Params.Arguments, &in)
	v, ok := in["result"]
	if !ok {
		return refusal(`deck: the arguments have no result field; call result_submit again with {"result": <the result>}.`), nil
	}
	s.got = append(json.RawMessage{}, v...)
	if err := s.Validate(v); err != nil {
		if !s.Retried {
			s.Retried = true
			return refusal("deck: the result does not match its schema: " + err.Error() +
				"\nDo not redo the work. Call result_submit again with only the corrected JSON."), nil
		}
		s.done = true
		return refusal("deck: the result still does not match its schema; deck keeps it marked as not by the schema. End your turn."), nil
	}
	s.done, s.taken = true, true
	if s.Accepted != nil {
		s.Accepted(s.got)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "deck: result accepted. End your turn."}}}, nil
}

func refusal(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}
