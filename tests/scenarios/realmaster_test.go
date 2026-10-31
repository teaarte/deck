package scenarios

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider/claudecode"
	"deck2/internal/provider/codex"
)

// masterReplies waits for n replies of the master in a topic.
func (e *Env) masterReplies(topic int64, n int) []chatMessage {
	e.T.Helper()
	var said []chatMessage
	defer func() {
		if len(said) >= n {
			return
		}
		for _, m := range e.chatMessages(topic) {
			if m.Reply != nil {
				_, log := e.Call("deck.process_log", map[string]any{"process": m.Reply.Process, "limit": 30})
				e.T.Logf("message %d: reply %+v, log %s", m.ID, *m.Reply, log)
			}
		}
	}()
	e.Until(fmt.Sprintf("reply %d of the master in topic %d", n, topic), func() bool {
		said = nil
		failed := ""
		for _, m := range e.chatMessages(topic) {
			if m.Role == "master" {
				said = append(said, m)
			}
			if m.Reply != nil && (m.Reply.State == "error" || m.Reply.State == "stopped") {
				failed = m.Reply.Error
			}
		}
		if len(said) >= n {
			return true
		}
		if failed != "" {
			e.T.Fatalf("the reply failed: %s", failed)
		}
		return false
	})
	return said
}

// replySession is the session of the reply process that took the n-th message of the owner.
func (e *Env) replySession(topic int64, n int) (string, string) {
	e.T.Helper()
	i := 0
	for _, m := range e.chatMessages(topic) {
		if m.Role == "owner" {
			if i++; i == n {
				p, err := e.App.Store.Q.GetProcess(context.Background(), m.Process)
				if err != nil {
					e.T.Fatal(err)
				}
				return p.SessionID, p.Provider
			}
		}
	}
	e.T.Fatalf("no message %d of the owner", n)
	return "", ""
}

// TestRealMasterTopicOnClaudeCode has the master on the installed Claude Code put a task into a project from a
// chat message, then answer a second message in the same session.
func TestRealMasterTopicOnClaudeCode(t *testing.T) {
	realCLI(t)
	home := t.TempDir()
	e := New(t, WithHome(home), WithProvider(claudecode.New(home)), WithConfig(realWaits), WithWait(5*time.Minute))
	e.Must("deck.project_create", map[string]any{"name": "groceries", "prompt": "# Groceries: the shopping list of the household.\n", "manifest": tasksManifest}, nil)
	e.Must("deck.project_create", map[string]any{"name": "garden", "prompt": "# Garden: work in the garden and its tools.\n"}, nil)
	topic := e.say(0, "We are out of oat milk, put it on the list.")
	first := e.masterReplies(topic, 1)
	t.Logf("master: %s", first[0].Text)
	var tasks []recordRow
	e.Must("deck.records_list", map[string]any{"project": "groceries", "collection": "tasks"}, &tasks)
	if len(tasks) != 1 || !strings.Contains(strings.ToLower(fmt.Sprint(tasks[0].Data["title"])), "milk") {
		t.Fatalf("the master puts the task into the tasks of groceries: %+v", tasks)
	}
	e.say(topic, "Which project did you put it into? Answer with the project name only.")
	second := e.masterReplies(topic, 2)
	t.Logf("master: %s", second[1].Text)
	if !strings.Contains(strings.ToLower(second[1].Text), "groceries") {
		t.Fatalf("the second reply knows the first: %q", second[1].Text)
	}
	s1, _ := e.replySession(topic, 1)
	s2, _ := e.replySession(topic, 2)
	if s1 == "" || s1 != s2 {
		t.Fatalf("both replies are one session: %q %q", s1, s2)
	}
}

// TestRealMasterSwitchesProvider moves a topic from Claude Code to Codex and back: Codex goes on from the
// retelling, Claude Code from its own session.
func TestRealMasterSwitchesProvider(t *testing.T) {
	realCLI(t)
	realCodex(t)
	home := t.TempDir()
	e := New(t, WithHome(home), WithProvider(claudecode.New(home), codex.New(home)), WithConfig(realWaits), WithWait(5*time.Minute))
	topic := e.say(0, "Remember: the code word of this conversation is PELICAN. Reply with OK only.")
	e.masterReplies(topic, 1)
	e.UseModels("master.reply", "codex")
	e.say(topic, "What is the code word of this conversation? Reply with the word only.")
	onCodex := e.masterReplies(topic, 2)[1]
	t.Logf("codex: %s", onCodex.Text)
	if !strings.Contains(strings.ToUpper(onCodex.Text), "PELICAN") {
		t.Fatalf("Codex knows the topic from the retelling: %q", onCodex.Text)
	}
	if _, pv := e.replySession(topic, 2); pv != "codex" {
		t.Fatalf("the second reply ran on codex: %s", pv)
	}
	e.UseModels("master.reply")
	e.say(topic, "The second code word is ZEBRA. Reply with both code words, separated by a space, and nothing else.")
	back := e.masterReplies(topic, 3)[2]
	t.Logf("claude-code: %s", back.Text)
	if up := strings.ToUpper(back.Text); !strings.Contains(up, "PELICAN") || !strings.Contains(up, "ZEBRA") {
		t.Fatalf("Claude Code goes on with the topic: %q", back.Text)
	}
	s1, _ := e.replySession(topic, 1)
	s3, pv := e.replySession(topic, 3)
	if pv != "claude-code" || s1 != s3 {
		t.Fatalf("back on Claude Code its first session goes on: %s %q %q", pv, s1, s3)
	}
}
