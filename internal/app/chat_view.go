package app

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

// waitsFor says what a reply waits for, by its state.
var waitsFor = map[string]string{
	process.StateWaitingAnswer: "the owner", process.StateWaitingSlot: "a free slot", process.StateWaitingProvider: "its provider",
	process.StateRateLimited: "the usage limit to reset", process.StateNeedsLogin: "a login", process.StateWaitingFolder: "a folder",
}

func chatAction(verb, command string, params map[string]any) map[string]any {
	return map[string]any{"verb": verb, "command": command, "params": params}
}

func openProcess(id int64) map[string]any {
	return map[string]any{"verb": "open", "screen": "process", "record": id}
}

func chatReply(p db.Process) map[string]any {
	r := map[string]any{"process": p.ID, "state": p.State, "provider": p.Provider, "open": openProcess(p.ID)}
	if p.Error != "" {
		r["error"] = untrusted.Cut(p.Error, 500)
	}
	return r
}

func (a *App) chatTopicItem(ctx context.Context, t db.ChatTopic) map[string]any {
	v := map[string]any{"id": t.ID, "title": t.Title, "archived": t.Archived == 1, "updated_at": t.UpdatedAt}
	if t.CardID != 0 {
		v["card"] = t.CardID
	}
	if last, err := a.Store.Q.LastMessages(ctx, db.LastMessagesParams{TopicID: t.ID, Before: 1 << 62, Lim: 1}); err == nil && len(last) == 1 {
		v["last"] = untrusted.Cut(last[0].Text, 200)
	}
	if p, err := a.Store.Q.OpenTopicProcess(ctx, t.ID); err == nil {
		v["reply"] = chatReply(p)
	}
	return v
}

// Chat is what a chat block shows: the topics, and of the topic picked its messages, the questions of its reply
// and the lines of the turn going on; the topic is an id, "new" for none, empty for the newest.
func (a *App) Chat(ctx context.Context, workspace int64, pick string) (any, error) {
	active, err := a.Store.Q.ListTopics(ctx, db.ListTopicsParams{WorkspaceID: workspace, Archived: 0, Lim: 50})
	if err != nil {
		return nil, err
	}
	archived, err := a.Store.Q.ListTopics(ctx, db.ListTopicsParams{WorkspaceID: workspace, Archived: 1, Lim: 20})
	if err != nil {
		return nil, err
	}
	v := map[string]any{"topics": []any{}, "archived": []any{}, "messages": []any{}, "questions": []any{},
		"send": chatAction("send", registry.Builtin+".chat_send", map[string]any{})}
	for _, t := range active {
		v["topics"] = append(v["topics"].([]any), a.chatTopicItem(ctx, t))
	}
	for _, t := range archived {
		v["archived"] = append(v["archived"].([]any), a.chatTopicItem(ctx, t))
	}
	var t db.ChatTopic
	switch id, _ := strconv.ParseInt(pick, 10, 64); {
	case pick == "new":
		return v, nil
	case id != 0:
		if t, err = a.Store.Q.GetTopic(ctx, id); err != nil || t.WorkspaceID != workspace {
			return v, nil
		}
	case len(active) > 0:
		t = active[0]
	default:
		return v, nil
	}
	v["topic"] = a.chatTopicItem(ctx, t)
	v["send"] = chatAction("send", registry.Builtin+".chat_send", map[string]any{"topic": t.ID})
	v["archive"] = chatAction("archive", registry.Builtin+".chat_topic_archive", map[string]any{"topic": t.ID, "archived": t.Archived == 0})
	if t.Archived == 1 {
		v["archive"].(map[string]any)["verb"] = "unarchive"
	}
	del := chatAction("delete", registry.Builtin+".chat_topic_delete", map[string]any{"topic": t.ID})
	del["confirm"] = true
	v["delete"] = del
	rows, err := a.Store.Q.LastMessages(ctx, db.LastMessagesParams{TopicID: t.ID, Before: 1 << 62, Lim: 100})
	if err != nil {
		return nil, err
	}
	replies := map[int64]map[string]any{}
	var ms []any
	for _, m := range rows {
		line := map[string]any{"id": m.ID, "role": m.Role, "text": m.Text, "created_at": m.CreatedAt}
		if m.Context != "" {
			line["screen"] = json.RawMessage(m.Context)
		}
		if m.Role == roleOwner && m.ProcessID != 0 {
			if _, seen := replies[m.ProcessID]; !seen {
				replies[m.ProcessID] = nil
				if p, err := a.Store.Q.GetProcess(ctx, m.ProcessID); err == nil {
					replies[m.ProcessID] = chatReply(p)
				}
			}
			if r := replies[m.ProcessID]; r != nil {
				line["reply"] = r
			}
		}
		ms = append(ms, line)
	}
	if ms != nil {
		v["messages"] = ms
	}
	open, err := a.Store.Q.OpenTopicProcess(ctx, t.ID)
	if err != nil {
		return v, nil
	}
	turn := map[string]any{"process": open.ID, "state": open.State, "provider": open.Provider, "lines": a.turnLines(open.ID),
		"stop": chatAction("stop", registry.Builtin+".process_stop", map[string]any{"process": open.ID}), "open": openProcess(open.ID)}
	if w := waitsFor[open.State]; w != "" {
		turn["waits"] = w
	}
	v["turn"] = turn
	asks, err := a.Store.Q.ListOpenAsksOfProcess(ctx, open.ID)
	if err != nil {
		return nil, err
	}
	var qs []any
	for _, q := range asks {
		if q.Kind != ask.KindQuestion {
			continue
		}
		var payload struct {
			Questions []json.RawMessage `json:"questions"`
		}
		_ = json.Unmarshal([]byte(q.Payload), &payload)
		questions := []any{}
		for _, raw := range payload.Questions {
			var one map[string]any
			if json.Unmarshal(raw, &one) == nil {
				questions = append(questions, one)
			}
		}
		answer := chatAction("answer", registry.Builtin+".ask_answer", map[string]any{"ask": q.ID})
		answer["input"] = "answer"
		qs = append(qs, map[string]any{"ask": q.ID, "text": q.Text, "questions": questions, "answer": answer})
	}
	if qs != nil {
		v["questions"] = qs
	}
	return v, nil
}

// turnLines are the last calls and messages in the log of a process still open.
func (a *App) turnLines(processID int64) []any {
	out := []any{}
	lines, _ := a.Runner.Tail(processID, chatLines, "tool", "tool_update", "message")
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
