package chat

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"deck2/internal/app/access"
	"deck2/internal/ask"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store"
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
	Project   string     `json:"project,omitempty"`
	Archived  bool       `json:"archived"`
	UpdatedAt string     `json:"updated_at"`
	Last      string     `json:"last,omitempty"`
	Model     string     `json:"model,omitempty"`
	Reply     *replyView `json:"reply,omitempty"`
}

// replyView is the process that answers a message, with its state and a button that opens it.
type replyView struct {
	Process  int64  `json:"process"`
	State    string `json:"state"`
	Provider string `json:"provider"`
	Error    string `json:"error,omitempty"`
	Open     action `json:"open"`
	process.Shown
	// Cut is what a reply that broke off left, with its undo and the providers it can go again on.
	Cut *cutView `json:"cut,omitempty"`
}

type messageView struct {
	ID      int64           `json:"id"`
	Role    string          `json:"role"`
	Text    string          `json:"text"`
	Screen  json.RawMessage `json:"screen,omitempty"`
	Process int64           `json:"process,omitempty"`
	Reply   *replyView      `json:"reply,omitempty"`
	Files   []fileView      `json:"files,omitempty"`
	// Work is how the reply went, on the last message of the master of each reply.
	Work      *workView `json:"work,omitempty"`
	CreatedAt string    `json:"created_at"`
	// Delete takes back a message of the owner no reply took yet.
	Delete action `json:"delete,omitempty"`
}

// fileView is a file of a message: one the owner attached, or one the reply and the processes it started made.
type fileView struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Size int64  `json:"size"`
	URL  string `json:"url"`
}

func viewFile(f db.File) fileView {
	return fileView{ID: f.ID, Name: f.Name, Type: f.Type, Size: f.Size, URL: fmt.Sprintf("/api/files/%d", f.ID)}
}

// made are the files a reply and the processes it started kept, in order.
func (ch *Service) made(ctx context.Context, p db.Process) []fileView {
	var out []fileView
	fs, _ := ch.St.Q.ListMadeFiles(ctx, p.ID)
	for _, f := range fs {
		out = append(out, viewFile(f))
	}
	return out
}

type questionView struct {
	Ask       int64            `json:"ask"`
	Text      string           `json:"text"`
	Questions []map[string]any `json:"questions"`
	Answer    action           `json:"answer"`
}

type turnView struct {
	Process   int64            `json:"process"`
	State     string           `json:"state"`
	Provider  string           `json:"provider"`
	StartedAt string           `json:"started_at,omitempty"`
	Lines     []map[string]any `json:"lines"`
	Processes []procView       `json:"processes"`
	Stop      action           `json:"stop"`
	Open      action           `json:"open"`
	process.Shown
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
	// Models are those the master's replies may go on; Model is the master's own, which a topic without one takes.
	Models []string `json:"models"`
	Model  string   `json:"model,omitempty"`
	Choose action   `json:"choose,omitempty"`
}

func viewReply(p db.Process, tr func(string) string) *replyView {
	return &replyView{Process: p.ID, State: p.State, Provider: p.Provider, Error: untrusted.Cut(p.Error, 500), Open: openProcess(p.ID),
		Shown: process.Show(p, "", false, tr)}
}

func viewMessage(m db.ChatMessage) messageView {
	v := messageView{ID: m.ID, Role: m.Role, Text: m.Text, Process: m.ProcessID, CreatedAt: m.CreatedAt}
	if m.Role == roleOwner && m.ProcessID == 0 {
		v.Delete = chatAction("delete", registry.Builtin+".chat_message_delete", map[string]any{"message": m.ID})
	}
	if m.Context != "" {
		v.Screen = json.RawMessage(m.Context)
	}
	return v
}

func viewTopic(t db.ListTopicSummariesRow, tr func(string) string) topicView {
	v := topicView{ID: t.ID, Title: t.Title, Project: t.Project, Archived: t.Archived == 1,
		UpdatedAt: t.UpdatedAt, Last: untrusted.Cut(t.LastText, 200), Model: t.Model}
	if t.OpenID != 0 {
		v.Reply = viewReply(db.Process{ID: t.OpenID, Kind: process.KindAgent, State: t.OpenState, Provider: t.OpenProvider, Error: t.OpenError}, tr)
	}
	return v
}

// topics are the summaries of the topics of a workspace, newest first; with topic, that one alone.
func (ch *Service) topics(ctx context.Context, workspace int64, archived bool, topic, lim int64, tr func(string) string) ([]topicView, error) {
	rows, err := ch.St.Q.ListTopicSummaries(ctx, db.ListTopicSummariesParams{WorkspaceID: workspace, Archived: store.Bool(archived), Topic: topic, Lim: lim})
	if err != nil {
		return nil, err
	}
	out := make([]topicView, 0, len(rows))
	for _, t := range rows {
		out = append(out, viewTopic(t, tr))
	}
	return out, nil
}

// messages are messages of a topic with the reply each message of the owner got and the files it has.
func (ch *Service) messages(ctx context.Context, topic int64, rows []db.ChatMessage, tr func(string) string) []messageView {
	replies := map[int64]*replyView{}
	files := map[int64][]fileView{}
	if fs, err := ch.St.Q.ListMessageFiles(ctx, topic); err == nil {
		for _, f := range fs {
			files[f.OwnerID] = append(files[f.OwnerID], viewFile(f))
		}
	}
	ch.mu.Lock()
	unstarted := ch.unstarted[topic]
	ch.mu.Unlock()
	last := lastOf(rows)
	out := make([]messageView, 0, len(rows))
	for _, r := range rows {
		m := viewMessage(r)
		if last[r.ProcessID] == r.ID && r.Role == roleMaster {
			if p, err := ch.St.Q.GetProcess(ctx, r.ProcessID); err == nil {
				m.Work = ch.work(ctx, p, tr)
				files[r.ID] = append(files[r.ID], ch.made(ctx, p)...)
			}
		}
		m.Files = files[r.ID]
		if r.Role == roleOwner && r.ProcessID == 0 && unstarted != "" {
			m.Reply = &replyView{State: process.StateError, Error: untrusted.Cut("the reply did not start: "+unstarted, 500),
				Shown: process.Show(db.Process{State: process.StateError}, "", false, tr)}
		}
		if r.Role == roleOwner && r.ProcessID != 0 {
			if _, seen := replies[r.ProcessID]; !seen {
				replies[r.ProcessID] = nil
				if p, err := ch.St.Q.GetProcess(ctx, r.ProcessID); err == nil {
					replies[r.ProcessID] = viewReply(p, tr)
					replies[r.ProcessID].Cut = ch.cut(ctx, p, r.ID)
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
	Lang  string `opt:"" desc:"Language of the words of the states of replies, such as ru; English without one"`
}

// chat is the chat with the master as a chat block shows it.
func (ch *Service) chat(ctx context.Context, act registry.Actor, in chatIn) (chatView, error) {
	v := chatView{Messages: []messageView{}, Questions: []questionView{}, Send: chatAction("send", registry.Builtin+".chat_send", map[string]any{})}
	var err error
	ws := ch.Words(in.Lang)
	tr := func(phrase string) string { return manifest.Tr(ws, phrase) }
	if v.Topics, err = ch.topics(ctx, act.Workspace, false, 0, 50, tr); err != nil {
		return v, err
	}
	if v.Archived, err = ch.topics(ctx, act.Workspace, true, 0, 20, tr); err != nil {
		return v, err
	}
	v.Models, v.Model = ch.models(ctx, act.Workspace)
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
	picked, err := ch.topics(ctx, act.Workspace, false, id, 1, tr)
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
	v.Choose = chatAction("model", registry.Builtin+".chat_topic_model", map[string]any{"topic": t.ID})
	v.Choose["input"] = "model"
	v.Delete = chatAction("delete", registry.Builtin+".chat_topic_delete", map[string]any{"topic": t.ID})
	v.Delete["confirm"] = true
	rows, err := ch.St.Q.LastMessages(ctx, db.LastMessagesParams{TopicID: t.ID, Before: 1 << 62, Lim: 100})
	if err != nil {
		return v, err
	}
	v.Messages = ch.messages(ctx, t.ID, rows, tr)
	if t.Reply == nil {
		return v, nil
	}
	open := t.Reply
	v.Turn = &turnView{Process: open.Process, State: open.State, Provider: open.Provider, Processes: ch.children(ctx, open.Process, false, tr),
		Lines: ch.turnLines(open.Process), Stop: chatAction("stop", registry.Builtin+".process_stop", map[string]any{"process": open.Process}),
		Open: openProcess(open.Process), Shown: open.Shown}
	if p, err := ch.St.Q.GetProcess(ctx, open.Process); err == nil {
		v.Turn.StartedAt = cmp.Or(p.ReopenedAt.String, p.StartedAt)
	}
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

// models are the models the master's replies may go on and the one they go on without a model of the topic.
func (ch *Service) models(ctx context.Context, workspace int64) ([]string, string) {
	m, err := access.Master(ctx, ch.St.Q, workspace)
	if err != nil {
		return []string{}, ""
	}
	cmd, err := ch.replyCommand(ctx, m.ID)
	if err != nil {
		return []string{}, ""
	}
	own := ""
	if pv, err := ch.For(ctx, m.ID, cmd); err == nil {
		own = pv.Name()
	}
	return ch.Able(ctx, workspace, cmd), own
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
