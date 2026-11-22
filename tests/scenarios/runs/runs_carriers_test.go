package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/tests/harness"
)

// carrierScenarios are, for each carrier of the mark, the scenarios that seed it with text from outside and see the turn
// it reaches marked.
var carrierScenarios = map[string][]string{
	"the input of process %d, which read %s":                {"TestTheMastersReaderReadsTheWebFreelyAndAMarkedMasterAsksOnceForIt"},
	"the work it continues of process %d, which read %s":    {"TestAHandedOnProcessKeepsTheMarkOfTheOneItContinues", "TestARetryOfAMarkedProcessKeepsItsMark"},
	"the result of process %d of its record, which read %s": {"TestTheThreadOfAMarkedRecordMarksTheNextTurnOfIt"},
	"topic %d: process %d, which read %s":                   {"TestATopicThatHoldsTheWordsOfAMarkedProcessMarksItsNextReply"},
	"a note of deck that quotes process %d, which read %s":  {"TestANoteThatQuotesAMarkedProcessMarksTheMastersTurn"},
	"a remark of process %d, which read %s":                 {"TestAMarkedTurnsRemarkMarksTheProcessItSendsBack"},
	"a remark of automation token %s":                       {"TestAnUntrustedTokensRemarkMarksTheProcessItSendsBack"},
	"the input of automation token %s":                      {"TestATokenOfOneCommandMarksItsModuleAndTheRunItStarts"},
	"the memory of %s, where %s wrote":                      {"TestAPoisonedMemoryMarksTheTurnsItReaches"},
	"the skills of %s, where %s wrote":                      {"TestTheSkillsOfAMarkedModuleOrWorkspaceMarkTheTurnsTheyReach"},
	"the skills of the workspace in %s, where %s wrote":     {"TestTheSkillsOfAMarkedModuleOrWorkspaceMarkTheTurnsTheyReach"},
}

// TestEveryCarrierOfTheMarkHasItsScenario: each kind of text deck puts into a turn that carries the mark names
// scenarios that exist, and the table names no carrier deck does not have.
func TestEveryCarrierOfTheMarkHasItsScenario(t *testing.T) {
	t.Parallel()
	known := map[string]bool{}
	decl := regexp.MustCompile(`(?m)^func (Test\w+)\(`)
	err := filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		b, err := os.ReadFile(path)
		for _, m := range decl.FindAllStringSubmatch(string(b), -1) {
			known[m[1]] = true
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	carriers := process.Carriers()
	for _, c := range carriers {
		if len(carrierScenarios[c]) == 0 {
			t.Errorf("the carrier %q has no scenario", c)
		}
		for _, name := range carrierScenarios[c] {
			if !known[name] {
				t.Errorf("the carrier %q names %s, which is no test under tests/scenarios", c, name)
			}
		}
	}
	for c := range carrierScenarios {
		if !slices.Contains(carriers, c) {
			t.Errorf("the table names %q, which is no carrier of deck", c)
		}
	}
}

// markOf is where the text from outside a process read came from, as its row keeps it.
func markOf(e *harness.Env, id int64) string {
	e.T.Helper()
	var read string
	// deck shows the mark of a process only in the owner's words, so the test reads the phrase it keeps.
	if err := e.SQLRow(`SELECT read_untrusted FROM process WHERE id = ?`, id).Scan(&read); err != nil {
		e.T.Fatal(err)
	}
	return read
}

// TestTheThreadOfAMarkedRecordMarksTheNextTurnOfIt: the next agent of a record a marked process worked on reads what
// that one did, so it is marked by it; an agent of another record is not.
func TestTheThreadOfAMarkedRecordMarksTheNextTurnOfIt(t *testing.T) {
	t.Parallel()
	e := harness.ClipsEnv(t, func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"done": "moved the crown"}`)}
	})
	a, b := e.Clip("a"), e.Clip("b")
	first := e.Start("clips", "revise", map[string]any{"record": a, "note": "the crown"})
	e.WaitState(first, "done")
	e.App.Runner.Wait()
	// Text from outside the first agent read, seeded: what it read is not the point here.
	e.SQL(`UPDATE process SET read_untrusted = 'a page' WHERE id = ?`, first)
	elsewhere := e.Start("clips", "revise", map[string]any{"record": b, "note": "the sky"})
	e.WaitState(elsewhere, "done")
	e.App.Runner.Wait()
	second := e.Start("clips", "revise", map[string]any{"record": a, "note": "the background"})
	e.WaitState(second, "done")
	if got, want := markOf(e, second), fmt.Sprintf("the result of process %d of its record, which read a page", first); got != want {
		t.Fatalf("the next agent of the record is marked by its thread: %q, want %q", got, want)
	}
	if got := markOf(e, elsewhere); got != "" {
		t.Fatalf("an agent of another record is clean: %q", got)
	}
}

// TestATopicThatHoldsTheWordsOfAMarkedProcessMarksItsNextReply: the next reply of a topic whose messages hold the
// words of a marked process is marked by them; a reply in a new topic is not.
func TestATopicThatHoldsTheWordsOfAMarkedProcessMarksItsNextReply(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Text: "ok"}
	}))
	topic := e.Say(0, "hello")
	e.MasterSaid(topic, 1)
	e.App.Runner.Wait()
	var first int64
	// The process whose words the topic's message holds, which deck shows nowhere.
	if err := e.SQLRow(`SELECT process_id FROM chat_message WHERE topic_id = ? AND process_id != 0 ORDER BY id LIMIT 1`, topic).Scan(&first); err != nil {
		t.Fatal(err)
	}
	// Text from outside that process read, seeded.
	e.SQL(`UPDATE process SET read_untrusted = 'a page' WHERE id = ?`, first)
	e.Say(topic, "and now?")
	e.MasterSaid(topic, 2)
	var second int64
	// The second reply, whose id the test does not hold.
	if err := e.SQLRow(`SELECT MAX(id) FROM process WHERE command = 'reply'`).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if got, want := markOf(e, second), fmt.Sprintf("topic %d: process %d, which read a page", topic, first); second == first || got != want {
		t.Fatalf("the next reply of the topic is marked by its words: %d %q, want %q", second, got, want)
	}
	other := e.Say(0, "another thing")
	e.MasterSaid(other, 1)
	var third int64
	// The reply of the new topic, whose id the test does not hold.
	if err := e.SQLRow(`SELECT MAX(id) FROM process WHERE command = 'reply'`).Scan(&third); err != nil {
		t.Fatal(err)
	}
	if got := markOf(e, third); third == second || got != "" {
		t.Fatalf("a reply in a new topic is clean: %d %q", third, got)
	}
}

// TestTheSkillsOfAMarkedModuleOrWorkspaceMarkTheTurnsTheyReach: a skill of a module whose data holds text from outside
// marks the turn that gets it, and so does a skill of the workspace when the master's data holds some.
func TestTheSkillsOfAMarkedModuleOrWorkspaceMarkTheTurnsTheyReach(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Text: "ok"}
	}))
	own := strings.Replace(harness.PlainManifest, `"prompt": "Do the work."}`, `"prompt": "Do the work.", "skills": ["reader"]}`, 1)
	e.Project("plain", own)
	e.Must("deck.document_save", map[string]any{"project": "plain", "name": "skills/reader/SKILL.md", "kind": "skill",
		"text": "---\nname: reader\ndescription: Reads.\n---\nRead.\n"}, nil)
	// Text from outside the data of the module holds, seeded.
	e.SQL(`UPDATE project SET untrusted = 'a page' WHERE name = 'plain'`)
	first := e.Start("plain", "work", nil)
	e.WaitState(first, "done")
	if got := markOf(e, first); got != "the skills of plain, where a page wrote" {
		t.Fatalf("the module's own skill marks its turn: %q", got)
	}
	shared := strings.Replace(harness.PlainManifest, `"prompt": "Do the work."}`, `"prompt": "Do the work.", "skills": ["memory"]}`, 1)
	e.Project("shared", shared)
	clean := e.Start("shared", "work", nil)
	e.WaitState(clean, "done")
	if got := markOf(e, clean); got != "" {
		t.Fatalf("a skill of a clean workspace marks nothing: %q", got)
	}
	// Text from outside the data of the master holds, seeded.
	e.SQL(`UPDATE project SET untrusted = 'a page' WHERE builtin = 1`)
	marked := e.Start("shared", "work", nil)
	e.WaitState(marked, "done")
	if got := markOf(e, marked); got != "the skills of the workspace in master, where a page wrote" {
		t.Fatalf("a skill of the workspace marks the turn once the master's data holds text from outside: %q", got)
	}
}

// TestTheWordsOfATopicComeFirstInTheMarkOfItsRunningReply: a reply the owner writes into while it runs, whose topic
// and a note it is told both quote a marked process, says the topic is where its text from outside came from.
func TestTheWordsOfATopicComeFirstInTheMarkOfItsRunningReply(t *testing.T) {
	t.Parallel()
	started, heard := make(chan struct{}, 1), make(chan string, 1)
	fake := &harness.Fake{Live: true, Script: func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "reply" && strings.Contains(c.Req.Prompt, "first words") {
			started <- struct{}{}
			heard <- harness.Heard(ctx, c)
		}
		return provider.Result{Text: "ok"}
	}}
	e := harness.New(t, harness.WithProvider(fake))
	e.Project("plain", harness.PlainManifest)
	x := e.Start("plain", "work", nil)
	e.WaitState(x, "done")
	topic := e.Say(0, "first words")
	<-started
	var reply int64
	e.Until("the reply to run", func() bool {
		// The reply, whose id the test does not hold.
		_ = e.SQLRow(`SELECT id FROM process WHERE command = 'reply' AND state = 'running'`).Scan(&reply)
		return reply != 0
	})
	// Text from outside the work read, seeded.
	e.SQL(`UPDATE process SET read_untrusted = 'a page' WHERE id = ?`, x)
	// The words of the work the topic holds, as deck writes them when it tells how a work ended.
	e.SQL(`INSERT INTO chat_message (topic_id, role, text, process_id) VALUES (?, 'deck', 'done', ?)`, topic, x)
	// A note to the master that quotes the work, as deck writes it when the work asks.
	e.SQL(`INSERT INTO feed (workspace_id, project_id, kind, author, text, for_master, process_id)
		VALUES (1, (SELECT id FROM project WHERE builtin = 1), 'note', 'deck', 'plain asked a thing', 1, ?)`, x)
	e.Say(topic, "more words")
	if got := <-heard; !strings.Contains(got, "more words") || !strings.Contains(got, "plain asked a thing") {
		t.Fatalf("the running reply hears the owner and the note: %q", got)
	}
	if got, want := markOf(e, reply), fmt.Sprintf("topic %d: process %d, which read a page", topic, x); got != want {
		t.Fatalf("the topic is where the running reply's text from outside came from: %q, want %q", got, want)
	}
}
