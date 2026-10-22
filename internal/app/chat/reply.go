package chat

import (
	"context"
	"database/sql"
	"deck2/internal/app/access"
	"deck2/master"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/google/uuid"

	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// chatQueue runs the replies of a topic one at a time; a pump of a topic whose reply runs, even from a hook that
// reply set off, leaves the topic for the running loop.
type chatQueue struct {
	busy, due map[int64]bool
}

// pump starts the reply to the owner's messages of a topic that no reply took yet.
func (ch *Service) pump(ctx context.Context, topic int64) {
	ch.mu.Lock()
	if ch.queue.due == nil {
		ch.queue.busy, ch.queue.due = map[int64]bool{}, map[int64]bool{}
	}
	ch.queue.due[topic] = true
	if ch.queue.busy[topic] {
		ch.mu.Unlock()
		return
	}
	ch.queue.busy[topic] = true
	for ch.queue.due[topic] {
		delete(ch.queue.due, topic)
		ch.mu.Unlock()
		err := ch.reply(ctx, topic)
		if err != nil && ctx.Err() == nil {
			slog.Error("chat reply", "topic", topic, "err", err)
		}
		ch.unstart(topic, err, ctx.Err() == nil)
		ch.mu.Lock()
	}
	delete(ch.queue.busy, topic)
	ch.mu.Unlock()
}

// unstart keeps why the reply of a topic did not start, so its messages show it, and forgets it once one does.
func (ch *Service) unstart(topic int64, err error, live bool) {
	ch.mu.Lock()
	was := ch.unstarted[topic]
	switch {
	case err == nil:
		delete(ch.unstarted, topic)
	case live:
		if ch.unstarted == nil {
			ch.unstarted = map[int64]string{}
		}
		ch.unstarted[topic] = err.Error()
	}
	now := ch.unstarted[topic]
	ch.mu.Unlock()
	if t, err := ch.St.Q.GetTopic(context.Background(), topic); was != now && err == nil {
		ch.Bus.Emit(events.Event{Workspace: t.WorkspaceID, Topic: "chat"})
	}
}

// PumpChats starts the replies to messages that a stop of deck left without one.
func (ch *Service) PumpChats(ctx context.Context) {
	ids, err := ch.St.Q.ListTopicsWithPending(ctx)
	if err != nil {
		slog.Error("chat topics with messages", "err", err)
		return
	}
	for _, id := range ids {
		ch.pump(ctx, id)
	}
}

// reply hands the waiting messages of a topic to the master: into its parked reply, or a new reply process
// that resumes the topic's session of its provider or starts one with a retelling.
func (ch *Service) reply(ctx context.Context, topicID int64) error {
	ch.start.Lock()
	defer ch.start.Unlock()
	t, err := ch.St.Q.GetTopic(ctx, topicID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	pending, err := ch.St.Q.ListPendingMessages(ctx, topicID)
	if err != nil || len(pending) == 0 {
		return err
	}
	first, last := pending[0].ID, pending[len(pending)-1].ID
	files := attached(pending)
	notes, told := ch.masterNotes(ctx, t.WorkspaceID)
	open, err := ch.St.Q.OpenTopicProcess(ctx, topicID)
	if err == nil {
		given := ""
		if len(files) > 0 {
			if given, err = ch.Runner.Give(ctx, open.ID, files); err != nil {
				return err
			}
		}
		if q := ch.openQuestion(ctx, open.ID); q != 0 {
			texts := make([]string, 0, len(pending)+1)
			for _, m := range pending {
				texts = append(texts, strings.TrimSpace(m.Text))
			}
			if given != "" {
				texts = append(texts, given)
			}
			if err := ch.Asks.Answer(ctx, q, strings.Join(texts, "\n\n")); err == nil {
				return ch.takeMessages(ctx, t, open.ID, open.SessionID, "", 0, last)
			}
		}
		if open.State == process.StateRunning {
			news, _, err := ch.chatPrompt(ctx, t, pending, "", "", notes, false)
			if err != nil || ch.Runner.Say(ctx, open.ID, strings.TrimSpace("deck: the owner wrote while you work.\n"+news+"\n"+given)) == process.Refused {
				return err
			}
			return ch.takeMessages(ctx, t, open.ID, open.SessionID, "", told, last)
		}
		if open.State != process.StateWaitingAnswer {
			return nil
		}
		msg, sign, err := ch.chatPrompt(ctx, t, pending, open.SessionID, open.Provider, notes, true)
		if err != nil {
			return err
		}
		if err := ch.Runner.Remark(ctx, open.ID, strings.TrimSpace(msg+"\n\n"+given)); err != nil {
			return err
		}
		return ch.takeMessages(ctx, t, open.ID, open.SessionID, sign, told, last)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	p, err := access.Master(ctx, ch.St.Q, t.WorkspaceID)
	if err != nil {
		return err
	}
	m, err := docs.Manifest(ctx, ch.St.Q, p)
	if err != nil {
		return err
	}
	reply := m.EventCommand(manifest.OnMessage)
	cmd := m.Command(reply)
	if cmd == nil {
		return registry.Invalid("deck: project master has no trigger on %s", manifest.OnMessage)
	}
	next := ch.againOf(topicID)
	if next.provider == "" && t.Model != "" {
		if !slices.Contains(ch.Able(ctx, p.WorkspaceID, cmd), t.Model) {
			return fmt.Errorf("the model %s of this topic does not run the master's replies now; pick another one at the top of the chat", t.Model)
		}
		next.provider = t.Model
	}
	pv, err := ch.For(ctx, p.ID, cmd)
	if next.provider != "" {
		pv, err = ch.Named(ctx, p.ID, next.provider)
	}
	if err != nil {
		return err
	}
	o := process.Origin{Session: uuid.NewString(), Files: files, Owners: true, Provider: next.provider}
	sign := ""
	s, err := ch.St.Q.GetChatSession(ctx, db.GetChatSessionParams{TopicID: topicID, Provider: pv.Name()})
	switch {
	case err == nil:
		o.Session, o.Resume = s.SessionID, true
		if o.Message, sign, err = ch.chatPrompt(ctx, t, pending, s.SessionID, pv.Name(), notes, true); err != nil {
			return err
		}
		o.Message = strings.TrimSpace(next.note + "\n\n" + o.Message)
	case errors.Is(err, sql.ErrNoRows):
		// A new session is told everything before the first waiting message, and the memory of now.
		if _, sign, err = ch.masterMemory(ctx, p, topicID); err != nil {
			return err
		}
		err = ch.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.SetChatSession(ctx, db.SetChatSessionParams{TopicID: topicID, Provider: pv.Name(), SessionID: o.Session,
				Seen: first - 1, Memory: sign})
		})
		if err != nil {
			return err
		}
	default:
		return err
	}
	retell, err := ch.retelling(ctx, t, first)
	if err != nil {
		return err
	}
	news, _, err := ch.chatPrompt(ctx, t, pending, "", "", notes, false)
	if err != nil {
		return err
	}
	o.Note = strings.TrimSpace(retell + "\n\n" + next.note + "\n\n" + news)
	id, err := ch.Runner.Start(ctx, p.ID, m, reply, map[string]any{"topic": topicID}, o)
	if err != nil {
		if id != 0 {
			// The failed reply takes the messages, so its error shows and the topic does not start again.
			_ = ch.takeMessages(ctx, t, id, o.Session, sign, 0, last)
		}
		return err
	}
	return ch.takeMessages(ctx, t, id, o.Session, sign, told, last)
}

// takeMessages gives the waiting messages up to through to the reply process, keeps the sign of the memory its
// session was given, when it was given one, and drops the notes of deck the turn was told, up to told.
func (ch *Service) takeMessages(ctx context.Context, t db.ChatTopic, processID int64, session, sign string, told, through int64) error {
	err := ch.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if told > 0 {
			if err := q.DeleteMasterNotes(ctx, db.DeleteMasterNotesParams{WorkspaceID: t.WorkspaceID, Through: told}); err != nil {
				return err
			}
		}
		if err := q.TakeMessages(ctx, db.TakeMessagesParams{ProcessID: processID, TopicID: t.ID, Through: through}); err != nil || sign == "" {
			return err
		}
		return q.SetChatMemory(ctx, db.SetChatMemoryParams{Memory: sign, TopicID: t.ID, SessionID: session})
	})
	return err
}

// masterNotes are the notes deck tells the master on its next turn, one a line, and the last of them.
func (ch *Service) masterNotes(ctx context.Context, workspace int64) (string, int64) {
	rows, _ := ch.St.Q.ListMasterNotes(ctx, workspace)
	lines := make([]string, 0, len(rows))
	last := int64(0)
	for _, r := range rows {
		lines, last = append(lines, r.Text), r.ID
	}
	return strings.Join(lines, "\n"), last
}

// chatPrompt is what a reply turn gets: on resume the messages its session missed, then the owner's new
// messages with their screens and where the summary of the topic is.
func (ch *Service) chatPrompt(ctx context.Context, t db.ChatTopic, pending []db.ChatMessage, session, provider, notes string, resume bool) (string, string, error) {
	type message struct{ Where, Text string }
	v := struct {
		MemoryChanged          bool
		Memory, Retold, Missed string
		Topic                  int64
		Pending                []message
		Summarized             bool
		Summary                string
		Grown                  int64
		Notes                  string
	}{Topic: t.ID, Summary: summaryName(t.ID)}
	if notes != "" {
		v.Notes = untrusted.Block("notes", notes)
	}
	sign := ""
	if resume {
		s, err := ch.St.Q.GetChatSession(ctx, db.GetChatSessionParams{TopicID: t.ID, Provider: provider})
		if err == nil && s.SessionID == session {
			mp, err := access.Master(ctx, ch.St.Q, t.WorkspaceID)
			if err != nil {
				return "", "", err
			}
			mem, now, err := ch.masterMemory(ctx, mp, t.ID)
			if err != nil {
				return "", "", err
			}
			sign = now
			v.MemoryChanged, v.Memory = now != s.Memory, mem
			missed, err := ch.St.Q.ListMessages(ctx, db.ListMessagesParams{TopicID: t.ID, After: s.Seen})
			if err != nil {
				return "", "", err
			}
			var lines []db.ChatMessage
			for _, m := range missed {
				if m.ID < pending[0].ID {
					lines = append(lines, m)
				}
			}
			switch {
			case len(lines) > retellTail:
				if v.Retold, err = ch.retelling(ctx, t, pending[0].ID); err != nil {
					return "", "", err
				}
			case len(lines) > 0:
				v.Missed = untrusted.Block("Messages new to you, oldest first", transcript(lines))
			}
		}
	}
	for _, m := range pending {
		v.Pending = append(v.Pending, message{Where: screenText(m.Context), Text: strings.TrimSpace(m.Text)})
	}
	grown, err := ch.sinceSummary(ctx, t)
	if err != nil {
		return "", "", err
	}
	since, _, err := ch.summary(ctx, t)
	if err != nil {
		return "", "", err
	}
	v.Summarized = since != ""
	if grown >= summaryDue {
		v.Grown = grown
	}
	return master.Text("turn", v), sign, nil
}

// sinceSummary counts the messages of a topic after the current revision of its summary.
func (ch *Service) sinceSummary(ctx context.Context, t db.ChatTopic) (int64, error) {
	since, _, err := ch.summary(ctx, t)
	if err != nil {
		return 0, err
	}
	return ch.St.Q.CountMessagesAfter(ctx, db.CountMessagesAfterParams{TopicID: t.ID, Since: since})
}

// summary is the current summary of a topic and when it was written; empty when the master wrote none.
func (ch *Service) summary(ctx context.Context, t db.ChatTopic) (since, text string, err error) {
	p, err := access.Master(ctx, ch.St.Q, t.WorkspaceID)
	if err != nil {
		return "", "", err
	}
	d, err := docs.Find(ctx, ch.St.Q, docs.Ref{Scope: docs.ProjectScope(p), Name: summaryName(t.ID)})
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	r, err := docs.Revision(ctx, ch.St.Q, d, 0)
	return r.CreatedAt, r.Text, err
}

// retelling is the start of a topic for a session that did not see it: the summary and the messages after it,
// at most retellTail, before the message before.
func (ch *Service) retelling(ctx context.Context, t db.ChatTopic, before int64) (string, error) {
	since, text, err := ch.summary(ctx, t)
	if err != nil {
		return "", err
	}
	tail, err := ch.St.Q.LastMessages(ctx, db.LastMessagesParams{TopicID: t.ID, Before: before, Lim: retellTail})
	if err != nil {
		return "", err
	}
	var after []db.ChatMessage
	for _, m := range tail {
		if m.CreatedAt >= since {
			after = append(after, m)
		}
	}
	procs := ch.topicProcesses(ctx, t)
	if strings.TrimSpace(text) == "" && len(after) == 0 && procs == "" {
		return "", nil
	}
	v := struct{ Summary, After, Processes string }{Processes: procs}
	if strings.TrimSpace(text) != "" {
		v.Summary = untrusted.Block("Summary of the topic", text)
	}
	if len(after) > 0 {
		v.After = untrusted.Block("Messages after the summary, word for word, oldest first", transcript(after))
	}
	return master.Text("retelling", v), nil
}

// topicLinks is how many processes the replies of a topic started a session that did not see them is told of.
const topicLinks = 10

// topicProcesses are the processes the replies of a topic started, newest first, one a line.
func (ch *Service) topicProcesses(ctx context.Context, t db.ChatTopic) string {
	m, err := access.Master(ctx, ch.St.Q, t.WorkspaceID)
	if err != nil {
		return ""
	}
	rows, err := ch.St.Q.ListTopicProcesses(ctx, db.ListTopicProcessesParams{MasterID: m.ID, Topic: t.ID, Lim: topicLinks})
	if err != nil || len(rows) == 0 {
		return ""
	}
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, process.Link(r.Process, r.Project+"."+r.Process.Command))
	}
	return untrusted.Block("Processes of the topic", strings.Join(lines, "\n"))
}

func transcript(ms []db.ChatMessage) string {
	var b strings.Builder
	for _, m := range ms {
		fmt.Fprintf(&b, "%s: %s\n", m.Role, strings.TrimSpace(m.Text))
	}
	return b.String()
}

// attached are the files the owner attached to messages.
func attached(ms []db.ChatMessage) []int64 {
	var out []int64
	for _, m := range ms {
		var s screenContext
		if json.Unmarshal([]byte(m.Context), &s) == nil {
			out = append(out, s.Files...)
		}
	}
	return out
}
