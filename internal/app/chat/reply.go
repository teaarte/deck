package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/google/uuid"

	"deck2/internal/access"
	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
	"deck2/master"
)

// chatQueue runs the replies of a topic one at a time; a pump of a topic whose reply runs, even from a hook that
// reply set off, leaves the topic for the running loop.
type chatQueue struct {
	busy, due map[int64]bool
}

// pump starts the reply to the owner's messages of a topic that no reply took yet.
func (s *Service) pump(ctx context.Context, topic int64) {
	s.mu.Lock()
	if s.queue.due == nil {
		s.queue.busy, s.queue.due = map[int64]bool{}, map[int64]bool{}
	}
	s.queue.due[topic] = true
	if s.queue.busy[topic] {
		s.mu.Unlock()
		return
	}
	s.queue.busy[topic] = true
	for s.queue.due[topic] {
		delete(s.queue.due, topic)
		s.mu.Unlock()
		err := s.reply(ctx, topic)
		if err != nil && ctx.Err() == nil {
			slog.Error("chat reply", "topic", topic, "err", err)
		}
		s.unstart(topic, err, ctx.Err() == nil)
		s.mu.Lock()
	}
	delete(s.queue.busy, topic)
	s.mu.Unlock()
}

// unstart keeps why the reply of a topic did not start, so its messages show it, and forgets it once one does.
func (s *Service) unstart(topic int64, err error, live bool) {
	s.mu.Lock()
	was := s.unstarted[topic]
	switch {
	case err == nil:
		delete(s.unstarted, topic)
	case live:
		if s.unstarted == nil {
			s.unstarted = map[int64]string{}
		}
		s.unstarted[topic] = err.Error()
	}
	now := s.unstarted[topic]
	s.mu.Unlock()
	if t, err := s.St.Q.GetTopic(context.Background(), topic); was != now && err == nil {
		s.Emit(events.Event{Workspace: t.WorkspaceID, Topic: events.Chat})
	}
}

// PumpChats starts the replies to messages that a stop of deck left without one.
func (s *Service) PumpChats(ctx context.Context) {
	ids, err := s.St.Q.ListTopicsWithPending(ctx)
	if err != nil {
		slog.Error("chat topics with messages", "err", err)
		return
	}
	for _, id := range ids {
		s.pump(ctx, id)
	}
}

// reply hands the waiting messages of a topic to the master: into its parked reply, or a new reply process
// that resumes the topic's session of its provider or starts one with a retelling.
func (s *Service) reply(ctx context.Context, topicID int64) error {
	s.start.Lock()
	defer s.start.Unlock()
	t, err := s.St.Q.GetTopic(ctx, topicID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	pending, err := s.St.Q.ListPendingMessages(ctx, topicID)
	if err != nil || len(pending) == 0 {
		return err
	}
	first, last := pending[0].ID, pending[len(pending)-1].ID
	files := attached(pending)
	notes, told := s.masterNotes(ctx, t.WorkspaceID)
	open, err := s.St.Q.OpenTopicProcess(ctx, topicID)
	if err == nil {
		given := ""
		if len(files) > 0 {
			if given, err = s.Runner.Give(ctx, open.ID, files); err != nil {
				return err
			}
		}
		if q := s.openQuestion(ctx, open.ID); q != 0 {
			texts := make([]string, 0, len(pending)+1)
			for _, m := range pending {
				texts = append(texts, strings.TrimSpace(m.Text))
			}
			if given != "" {
				texts = append(texts, given)
			}
			if err := s.Asks.Answer(ctx, q, strings.Join(texts, "\n\n")); err == nil {
				return s.takeMessages(ctx, t, open.ID, open.SessionID, "", 0, last)
			}
		}
		if open.State == process.StateRunning {
			news, _, err := s.chatPrompt(ctx, t, pending, "", "", notes, false)
			if err != nil || s.Runner.Say(ctx, open.ID, strings.TrimSpace("deck: the owner wrote while you work.\n"+news+"\n"+given)) == process.Refused {
				return err
			}
			return s.takeMessages(ctx, t, open.ID, open.SessionID, "", told, last)
		}
		if open.State != process.StateWaitingAnswer {
			return nil
		}
		msg, sign, err := s.chatPrompt(ctx, t, pending, open.SessionID, open.Provider, notes, true)
		if err != nil {
			return err
		}
		if err := s.Runner.Remark(ctx, open.ID, strings.TrimSpace(msg+"\n\n"+given)); err != nil {
			return err
		}
		return s.takeMessages(ctx, t, open.ID, open.SessionID, sign, told, last)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	p, err := access.Master(ctx, s.St.Q, t.WorkspaceID)
	if err != nil {
		return err
	}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return err
	}
	reply := m.EventCommand(manifest.OnMessage)
	cmd := m.Command(reply)
	if cmd == nil {
		return registry.Invalid("project master has no trigger on %s", manifest.OnMessage)
	}
	next := s.againOf(topicID)
	if next.provider == "" && t.Model != "" {
		if !slices.Contains(s.Able(ctx, p, cmd), t.Model) {
			return fmt.Errorf("the model %s of this topic does not run the master's replies now; pick another one at the top of the chat", t.Model)
		}
		next.provider = t.Model
	}
	pv, err := s.For(ctx, p.ID, cmd)
	if next.provider != "" {
		pv, err = s.Named(ctx, p.ID, next.provider)
	}
	if err != nil {
		return err
	}
	o := process.Origin{Session: uuid.NewString(), Files: files, Owners: true, Provider: next.provider, Follows: next.follows}
	sign := ""
	sess, err := s.St.Q.GetChatSession(ctx, db.GetChatSessionParams{TopicID: topicID, Provider: pv.Name()})
	switch {
	case err == nil:
		o.Session, o.Resume = sess.SessionID, true
		if o.Message, sign, err = s.chatPrompt(ctx, t, pending, sess.SessionID, pv.Name(), notes, true); err != nil {
			return err
		}
		o.Message = strings.TrimSpace(next.note + "\n\n" + o.Message)
	case errors.Is(err, sql.ErrNoRows):
		// A new session is told everything before the first waiting message, and the memory of now.
		if _, _, sign, err = s.masterMemory(ctx, p, topicID, nil, false); err != nil {
			return err
		}
		err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.SetChatSession(ctx, db.SetChatSessionParams{TopicID: topicID, Provider: pv.Name(), SessionID: o.Session,
				Seen: first - 1, Memory: sign})
		})
		if err != nil {
			return err
		}
	default:
		return err
	}
	retell, err := s.retelling(ctx, t, first)
	if err != nil {
		return err
	}
	news, _, err := s.chatPrompt(ctx, t, pending, "", "", notes, false)
	if err != nil {
		return err
	}
	o.Note = strings.TrimSpace(retell + "\n\n" + next.note + "\n\n" + news)
	id, err := s.Runner.Start(ctx, p.ID, m, reply, map[string]any{"topic": topicID}, o)
	if err != nil {
		if id != 0 {
			// The failed reply takes the messages, so its error shows and the topic does not start again.
			_ = s.takeMessages(ctx, t, id, o.Session, sign, 0, last)
		}
		return err
	}
	return s.takeMessages(ctx, t, id, o.Session, sign, told, last)
}

// takeMessages gives the waiting messages up to through to the reply process, keeps the sign of the memory its
// session was given, when it was given one, and drops the notes of deck the turn was told, up to told.
func (s *Service) takeMessages(ctx context.Context, t db.ChatTopic, processID int64, session, sign string, told, through int64) error {
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
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
func (s *Service) masterNotes(ctx context.Context, workspace int64) (string, int64) {
	rows, _ := s.St.Q.ListMasterNotes(ctx, workspace)
	lines := make([]string, 0, len(rows))
	last := int64(0)
	for _, r := range rows {
		lines, last = append(lines, r.Text), r.ID
	}
	return strings.Join(lines, "\n"), last
}

// chatPrompt is what a reply turn gets: on resume the messages its session missed, then the owner's new
// messages with their screens and where the summary of the topic is.
func (s *Service) chatPrompt(ctx context.Context, t db.ChatTopic, pending []db.ChatMessage, session, provider, notes string, resume bool) (string, string, error) {
	type message struct{ Where, Text string }
	v := struct {
		MemoryChanged          bool
		Memory, Retold, Missed string
		Topic                  int64
		Pending                []message
		Summarized             bool
		Summary                string
		Revision, After, Grown int64
		Notes                  string
		Projects, Open         string
	}{Topic: t.ID, Summary: summaryName(t.ID)}
	if notes != "" {
		v.Notes = untrusted.Block("notes", notes)
	}
	sign := ""
	if resume {
		sess, err := s.St.Q.GetChatSession(ctx, db.GetChatSessionParams{TopicID: t.ID, Provider: provider})
		if err == nil && sess.SessionID == session {
			mp, err := access.Master(ctx, s.St.Q, t.WorkspaceID)
			if err != nil {
				return "", "", err
			}
			mem, _, now, err := s.masterMemory(ctx, mp, t.ID, nil, false)
			if err != nil {
				return "", "", err
			}
			sign = now
			v.MemoryChanged, v.Memory = now != sess.Memory, mem
			missed, err := s.St.Q.ListMessages(ctx, db.ListMessagesParams{TopicID: t.ID, After: sess.Seen})
			if err != nil {
				return "", "", err
			}
			var lines []db.ChatMessage
			for _, m := range missed {
				if m.ID < pending[0].ID && !s.knows(ctx, m, session) {
					lines = append(lines, m)
				}
			}
			switch {
			case len(lines) > retellTail:
				if v.Retold, err = s.retelling(ctx, t, pending[0].ID); err != nil {
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
	sum, err := s.summary(ctx, t)
	if err != nil {
		return "", "", err
	}
	grown, err := s.St.Q.CountMessagesAfter(ctx, db.CountMessagesAfterParams{TopicID: t.ID, Since: sum.CreatedAt})
	if err != nil {
		return "", "", err
	}
	v.Summarized, v.Revision, v.After = sum.N > 0, sum.N, grown
	if grown >= summaryDue {
		v.Grown = grown
	}
	v.Projects, v.Open = s.projectIndex(ctx, t.WorkspaceID), s.openWork(ctx, t)
	return master.Text("turn", v), sign, nil
}

// summary is the current revision of the summary of a topic; empty when the master wrote none.
func (s *Service) summary(ctx context.Context, t db.ChatTopic) (db.DocumentRevision, error) {
	p, err := access.Master(ctx, s.St.Q, t.WorkspaceID)
	if err != nil {
		return db.DocumentRevision{}, err
	}
	d, err := docs.Find(ctx, s.St.Q, docs.Ref{Scope: docs.ProjectScope(p), Name: summaryName(t.ID)})
	if errors.Is(err, sql.ErrNoRows) {
		return db.DocumentRevision{}, nil
	}
	if err != nil {
		return db.DocumentRevision{}, err
	}
	return docs.Revision(ctx, s.St.Q, d, 0)
}

// retelling is the start of a topic for a session that did not see it: the summary and the messages after it,
// at most retellTail, before the message before.
func (s *Service) retelling(ctx context.Context, t db.ChatTopic, before int64) (string, error) {
	sum, err := s.summary(ctx, t)
	if err != nil {
		return "", err
	}
	since, text := sum.CreatedAt, sum.Text
	tail, err := s.St.Q.LastMessages(ctx, db.LastMessagesParams{TopicID: t.ID, Before: before, Lim: retellTail})
	if err != nil {
		return "", err
	}
	var after []db.ChatMessage
	for _, m := range tail {
		if m.CreatedAt >= since {
			after = append(after, m)
		}
	}
	procs := s.topicProcesses(ctx, t)
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
func (s *Service) topicProcesses(ctx context.Context, t db.ChatTopic) string {
	m, err := access.Master(ctx, s.St.Q, t.WorkspaceID)
	if err != nil {
		return ""
	}
	rows, err := s.St.Q.ListTopicProcesses(ctx, db.ListTopicProcessesParams{MasterID: m.ID, Topic: t.ID, Lim: topicLinks})
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
		if m.Role == roleDeck {
			var s toldState
			_ = json.Unmarshal([]byte(m.Context), &s)
			fmt.Fprintf(&b, "deck: process #%d %s: %s\n", m.ProcessID, s.State, strings.TrimSpace(m.Text))
			continue
		}
		fmt.Fprintf(&b, "%s: %s%s\n", m.Role, strings.TrimSpace(m.Text), shownLine(m))
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

// knows reports whether a session already has a message: its own reply, or one of the owner a turn of it took and
// answered; what deck wrote meanwhile and what a failed turn took it has not.
func (s *Service) knows(ctx context.Context, m db.ChatMessage, session string) bool {
	if m.ProcessID == 0 {
		return false
	}
	p, err := s.St.Q.GetProcess(ctx, m.ProcessID)
	if err != nil || p.SessionID != session {
		return false
	}
	return m.Role == roleMaster || p.State == process.StateDone
}
