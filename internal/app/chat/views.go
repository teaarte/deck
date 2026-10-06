package chat

import (
	"context"
	"encoding/json"
	"strconv"

	"deck2/internal/ask"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// chatLines is how many lines of the log of a running reply the chat shows.
const chatLines = 30

// action is a button of the chat: a verb and the command it calls, or a screen it opens.
type action map[string]any

func chatAction(verb, command string, params map[string]any) action {
	return action{"verb": verb, "command": command, "params": params}
}

func openProcess(id int64) action {
	return action{"verb": "open", "screen": "process", "record": id}
}

// topicView is a topic: its last message cut short, the module it is about and the reply still going.
type topicView struct {
	ID        int64      `json:"id"`
	Title     string     `json:"title"`
	Card      int64      `json:"card,omitempty"`
	Project   string     `json:"project,omitempty"`
	Archived  bool       `json:"archived"`
	UpdatedAt string     `json:"updated_at"`
	Last      string     `json:"last,omitempty"`
	Reply     *replyView `json:"reply,omitempty"`
}

// replyView is the process that answers a message, with its state and a button that opens it.
type replyView struct {
	Process  int64  `json:"process"`
	State    string `json:"state"`
	Provider string `json:"provider"`
	Error    string `json:"error,omitempty"`
	Open     action `json:"open"`
}

type messageView struct {
	ID        int64           `json:"id"`
	Role      string          `json:"role"`
	Text      string          `json:"text"`
	Screen    json.RawMessage `json:"screen,omitempty"`
	Process   int64           `json:"process,omitempty"`
	Reply     *replyView      `json:"reply,omitempty"`
	Files     []fileView      `json:"files,omitempty"`
	CreatedAt string          `json:"created_at"`
}

// fileView is a file the owner attached to a message.
type fileView struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type questionView struct {
	Ask       int64            `json:"ask"`
	Text      string           `json:"text"`
	Questions []map[string]any `json:"questions"`
	Answer    action           `json:"answer"`
}

type turnView struct {
	Process  int64            `json:"process"`
	State    string           `json:"state"`
	Provider string           `json:"provider"`
	Lines    []map[string]any `json:"lines"`
	Stop     action           `json:"stop"`
	Open     action           `json:"open"`
}

// chatView is what a chat block shows: the topics, and of the topic picked its messages, the questions of its
// reply and the lines of the turn going on.
type chatView struct {
	Topics    []topicView    `json:"topics"`
	Archived  []topicView    `json:"archived"`
	Topic     *topicView     `json:"topic,omitempty"`
	Messages  []messageView  `json:"messages"`
	Questions []questionView `json:"questions"`
	Turn      *turnView      `json:"turn,omitempty"`
	Send      action         `json:"send"`
	Archive   action         `json:"archive,omitempty"`
	Delete    action         `json:"delete,omitempty"`
}

func viewReply(p db.Process) *replyView {
	return &replyView{Process: p.ID, State: p.State, Provider: p.Provider, Error: untrusted.Cut(p.Error, 500), Open: openProcess(p.ID)}
}

func viewMessage(m db.ChatMessage) messageView {
	v := messageView{ID: m.ID, Role: m.Role, Text: m.Text, Process: m.ProcessID, CreatedAt: m.CreatedAt}
	if m.Context != "" {
		v.Screen = json.RawMessage(m.Context)
	}
	return v
}

func viewTopic(t db.ListTopicSummariesRow) topicView {
	v := topicView{ID: t.ID, Title: t.Title, Card: t.CardID.Int64, Project: t.Project, Archived: t.Archived == 1,
		UpdatedAt: t.UpdatedAt, Last: untrusted.Cut(t.LastText, 200)}
	if t.OpenID != 0 {
		v.Reply = viewReply(db.Process{ID: t.OpenID, State: t.OpenState, Provider: t.OpenProvider, Error: t.OpenError})
	}
	return v
}

// topics are the summaries of the topics of a workspace, newest first; with topic, that one alone.
func (ch *Service) topics(ctx context.Context, workspace int64, archived bool, topic, lim int64) ([]topicView, error) {
	rows, err := ch.St.Q.ListTopicSummaries(ctx, db.ListTopicSummariesParams{WorkspaceID: workspace, Archived: b2i(archived), Topic: topic, Lim: lim})
	if err != nil {
		return nil, err
	}
	out := make([]topicView, 0, len(rows))
	for _, t := range rows {
		out = append(out, viewTopic(t))
	}
	return out, nil
}

// messages are messages of a topic with the reply each message of the owner got and the files it has.
func (ch *Service) messages(ctx context.Context, topic int64, rows []db.ChatMessage) []messageView {
	replies := map[int64]*replyView{}
	files := map[int64][]fileView{}
	if fs, err := ch.St.Q.ListMessageFiles(ctx, topic); err == nil {
		for _, f := range fs {
			files[f.OwnerID] = append(files[f.OwnerID], fileView{ID: f.ID, Name: f.Name})
		}
	}
	ch.mu.Lock()
	unstarted := ch.unstarted[topic]
	ch.mu.Unlock()
	out := make([]messageView, 0, len(rows))
	for _, r := range rows {
		m := viewMessage(r)
		m.Files = files[r.ID]
		if r.Role == roleOwner && r.ProcessID == 0 && unstarted != "" {
			m.Reply = &replyView{State: process.StateError, Error: untrusted.Cut("the reply did not start: "+unstarted, 500)}
		}
		if r.Role == roleOwner && r.ProcessID != 0 {
			if _, seen := replies[r.ProcessID]; !seen {
				replies[r.ProcessID] = nil
				if p, err := ch.St.Q.GetProcess(ctx, r.ProcessID); err == nil {
					replies[r.ProcessID] = viewReply(p)
				}
			}
			m.Reply = replies[r.ProcessID]
		}
		out = append(out, m)
	}
	return out
}

type chatIn struct {
	Topic string `opt:"" desc:"Topic id; new for none, the newest without one"`
}

// chat is the chat with the master as a chat block shows it.
func (ch *Service) chat(ctx context.Context, act registry.Actor, in chatIn) (chatView, error) {
	v := chatView{Messages: []messageView{}, Questions: []questionView{}, Send: chatAction("send", registry.Builtin+".chat_send", map[string]any{})}
	var err error
	if v.Topics, err = ch.topics(ctx, act.Workspace, false, 0, 50); err != nil {
		return v, err
	}
	if v.Archived, err = ch.topics(ctx, act.Workspace, true, 0, 20); err != nil {
		return v, err
	}
	var id int64
	switch {
	case in.Topic == "new":
		return v, nil
	case in.Topic != "":
		if id, err = strconv.ParseInt(in.Topic, 10, 64); err != nil {
			return v, nil
		}
	case len(v.Topics) > 0:
		id = v.Topics[0].ID
	default:
		return v, nil
	}
	picked, err := ch.topics(ctx, act.Workspace, false, id, 1)
	if err != nil || len(picked) == 0 {
		return v, err
	}
	t := picked[0]
	v.Topic = &t
	v.Send = chatAction("send", registry.Builtin+".chat_send", map[string]any{"topic": t.ID})
	v.Archive = chatAction("archive", registry.Builtin+".chat_topic_archive", map[string]any{"topic": t.ID, "archived": !t.Archived})
	if t.Archived {
		v.Archive["verb"] = "unarchive"
	}
	v.Delete = chatAction("delete", registry.Builtin+".chat_topic_delete", map[string]any{"topic": t.ID})
	v.Delete["confirm"] = true
	rows, err := ch.St.Q.LastMessages(ctx, db.LastMessagesParams{TopicID: t.ID, Before: 1 << 62, Lim: 100})
	if err != nil {
		return v, err
	}
	v.Messages = ch.messages(ctx, t.ID, rows)
	if t.Reply == nil {
		return v, nil
	}
	open := t.Reply
	v.Turn = &turnView{Process: open.Process, State: open.State, Provider: open.Provider,
		Lines: ch.turnLines(open.Process), Stop: chatAction("stop", registry.Builtin+".process_stop", map[string]any{"process": open.Process}),
		Open: openProcess(open.Process)}
	asks, err := ch.St.Q.ListOpenAsksOfProcess(ctx, open.Process)
	if err != nil {
		return v, err
	}
	for _, q := range asks {
		if q.Kind != ask.KindQuestion {
			continue
		}
		var payload struct {
			Questions []json.RawMessage `json:"questions"`
		}
		_ = json.Unmarshal([]byte(q.Payload), &payload)
		questions := []map[string]any{}
		for _, raw := range payload.Questions {
			var one map[string]any
			if json.Unmarshal(raw, &one) == nil {
				questions = append(questions, one)
			}
		}
		answer := chatAction("answer", registry.Builtin+".ask_answer", map[string]any{"ask": q.ID})
		answer["input"] = "answer"
		v.Questions = append(v.Questions, questionView{Ask: q.ID, Text: q.Text, Questions: questions, Answer: answer})
	}
	return v, nil
}

// turnLines are the last calls and messages in the log of a process still open.
func (ch *Service) turnLines(processID int64) []map[string]any {
	out := []map[string]any{}
	lines, _ := ch.Runner.Tail(processID, chatLines, "tool", "tool_update", "message")
	for _, raw := range lines {
		var l map[string]any
		_ = json.Unmarshal(raw, &l)
		if text, ok := l["text"].(string); ok {
			l["text"] = untrusted.Cut(text, 300)
		}
		out = append(out, l)
	}
	return out
}
