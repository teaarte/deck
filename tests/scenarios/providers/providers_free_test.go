package providers

import (
	"context"
	"net/http"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestFreeModelsFirstAreAChainAsAnAssignment: sleep starts on the free agent model of a connection; refused at its
// limit, the turn goes on on the next model, and while the free one is paused the next sleep starts there.
func TestFreeModelsFirstAreAChainAsAnAssignment(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	llm := harness.NewLLM(t, func(n int, _ harness.ChatRequest) harness.ChatReply {
		if n <= 3 {
			return harness.TextReply("ok")
		}
		return harness.ChatReply{Status: 429, Header: http.Header{"Retry-After": {"3600"}}, Body: `{"error": {"message": "free tier used up"}}`}
	})
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		harness.AcpModel, `{"end": "end_turn"}`))
	e := harness.New(t, harness.WithAgent(func(context.Context, *harness.Call) provider.Result { return provider.Result{Text: "slept"} }),
		harness.WithACP(f, llm))
	e.MasterAwake()
	e.Must("deck.model_set", map[string]any{"connection": "acp", "model": "m", "name": "acp", "free": true}, nil)
	provOf := func(id int64) (p struct {
		Provider, State string
		ErrorType       string `json:"error_type"`
	}) {
		e.Must("deck.process_get", map[string]any{"process": id}, &p)
		return p
	}
	var r struct{ Process int64 }
	e.Must("master.sleep", map[string]any{}, &r)
	e.Until("the sleep goes on on the next model", func() bool {
		// The process a hand-on started, by the one it follows.
		next := e.SQLRow(`SELECT count(*) FROM process WHERE follows_id = ? AND state = 'done' AND provider = 'fake'`, r.Process)
		var n int
		return next.Scan(&n) == nil && n == 1
	})
	if p := provOf(r.Process); p.Provider != "acp" || p.State != "error" || p.ErrorType != "model.limit" {
		t.Fatalf("sleep started on the free model, which refused at its limit: %+v", p)
	}
	e.Must("master.sleep", map[string]any{}, &r)
	e.WaitState(r.Process, "done")
	if p := provOf(r.Process); p.Provider != "fake" {
		t.Fatalf("while the free model is paused sleep starts on the next: %+v", p)
	}
}
