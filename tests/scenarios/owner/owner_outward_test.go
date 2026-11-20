package owner

import (
	"net/http"
	"slices"
	"testing"

	"deck2/tests/harness"
)

// TestWhatTheOwnerSendsOutLeavesALine: the owner's call of a command that sends and of a tool that sends each keep a
// line of the journal with the right to send; a call of the owner that stays in deck, as its grant, keeps none.
func TestWhatTheOwnerSendsOutLeavesALine(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { _, _ = w.Write([]byte(`{"id": "g1"}`)) })
	e := harness.New(t, harness.WithClock())
	e.Project("clips", harness.GenerateManifest(svc.URL))
	e.Grant("clips", "send", "generate", "make")
	var run struct{ Process int64 }
	e.Must("clips.make", map[string]any{"topic": "cats"}, &run)
	e.WaitState(run.Process, "done", "error")
	e.Must("clips.generate", map[string]any{"topic": "dogs"}, nil)

	var got []string
	// The lines of the owner's calls, which the journal's screen shows by process.
	rows := e.SQLRows(`SELECT tool, need, subject, outcome FROM tool_call WHERE actor = 'owner' ORDER BY id`)
	for rows.Next() {
		var tool, need, subject, outcome string
		if err := rows.Scan(&tool, &need, &subject, &outcome); err != nil {
			t.Fatal(err)
		}
		got = append(got, tool+" "+need+" "+subject+" "+outcome)
	}
	_ = rows.Close()
	if want := []string{"clips.make send make done", "clips.generate send generate done"}; !slices.Equal(got, want) {
		t.Fatalf("the owner's lines are %q, want %q", got, want)
	}
}
