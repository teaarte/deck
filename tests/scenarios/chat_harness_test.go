package scenarios

import (
	"encoding/json"
	"maps"
	"strconv"
	"strings"
	"testing"
)

// tasksManifest is a project that keeps its tasks in a collection, as the master puts them there.
const tasksManifest = `{"version": 13,
  "collections": [{"name": "tasks", "description": "A task of the project: what to do.", "key": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "text": {"type": "string"}}, "required": ["title"]}}],
  "screens": [{"name": "tasks", "title": "Tasks", "blocks": [{"type": "table", "title": "Tasks", "source": {"collection": "tasks"},
    "columns": [{"label": "Task", "bind": "title"}]}]}],
  "menu": ["tasks"]}`

type topicReply struct {
	Process  int64  `json:"process"`
	State    string `json:"state"`
	Provider string `json:"provider"`
	Error    string `json:"error"`
	Cut      *struct {
		Did  []string `json:"did"`
		Undo []struct {
			Text   string `json:"text"`
			Action struct {
				Command string         `json:"command"`
				Params  map[string]any `json:"params"`
			} `json:"action"`
		} `json:"undo"`
		Retry []struct {
			Label   string         `json:"label"`
			Command string         `json:"command"`
			Params  map[string]any `json:"params"`
		} `json:"retry"`
	} `json:"cut"`
}

type chatMessage struct {
	ID      int64           `json:"id"`
	Role    string          `json:"role"`
	Text    string          `json:"text"`
	Screen  json.RawMessage `json:"screen"`
	Process int64           `json:"process"`
	Reply   *topicReply     `json:"reply"`
	Delete  map[string]any  `json:"delete"`
	Files   []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"files"`
}

type chatTopic struct {
	ID       int64       `json:"id"`
	Title    string      `json:"title"`
	Card     int64       `json:"card"`
	Archived bool        `json:"archived"`
	Last     string      `json:"last"`
	Reply    *topicReply `json:"reply"`
}

// say sends the owner's message to the master; topic 0 opens a new topic. It returns the topic.
func (e *Env) say(topic int64, text string, more ...map[string]any) int64 {
	e.T.Helper()
	in := map[string]any{"text": text}
	if topic != 0 {
		in["topic"] = topic
	}
	for _, m := range more {
		for k, v := range m {
			in[k] = v
		}
	}
	var out struct {
		Topic int64 `json:"topic"`
	}
	e.Must("deck.chat_send", in, &out)
	return out.Topic
}

func (e *Env) chatMessages(topic int64) []chatMessage {
	e.T.Helper()
	var out struct {
		Messages []chatMessage `json:"messages"`
	}
	e.chatRead(map[string]any{"topic": strconv.FormatInt(topic, 10)}, &out)
	return out.Messages
}

// chatTopics reads the topics of the chat, or its archive, into out.
func (e *Env) chatTopics(archived bool, out any) {
	e.T.Helper()
	var view struct {
		Topics   json.RawMessage `json:"topics"`
		Archived json.RawMessage `json:"archived"`
	}
	e.Must("deck.chat_topics", nil, &view)
	raw := view.Topics
	if archived {
		raw = view.Archived
	}
	if err := json.Unmarshal(raw, out); err != nil {
		e.T.Fatal(err)
	}
}

// masterSaid waits until the topic has n replies of the master and returns them.
func (e *Env) masterSaid(topic int64, n int) []chatMessage {
	e.T.Helper()
	var said []chatMessage
	e.Until("the master's replies", func() bool {
		said = nil
		for _, m := range e.chatMessages(topic) {
			if m.Role == "master" {
				said = append(said, m)
			}
		}
		return len(said) >= n
	})
	return said
}

// lastLine is the last non-empty line of a prompt the fake got.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// projectsOf lists the projects other than the built-in one into out.
func (e *Env) projectsOf(out any) {
	e.T.Helper()
	var all []map[string]any
	e.Must("deck.projects_list", nil, &all)
	mine := []map[string]any{}
	for _, p := range all {
		if p["builtin"] != true {
			mine = append(mine, p)
		}
	}
	b, _ := json.Marshal(mine)
	if err := json.Unmarshal(b, out); err != nil {
		e.T.Fatal(err)
	}
}

// menuOf is the menu as one list without the built-in master: deck's screens, then the projects.
func (e *Env) menuOf(out any) {
	e.T.Helper()
	e.menuList(nil, false, out)
}

// menuList is the menu as one list: deck's screens, the projects and, with master, the master.
func (e *Env) menuList(in map[string]any, master bool, out any) {
	e.T.Helper()
	var m struct {
		Deck     json.RawMessage
		Projects []json.RawMessage
		Master   json.RawMessage
	}
	e.Must("deck.menu", in, &m)
	all := append([]json.RawMessage{m.Deck}, m.Projects...)
	if master && m.Master != nil {
		all = append(all, m.Master)
	}
	b, _ := json.Marshal(all)
	if err := json.Unmarshal(b, out); err != nil {
		e.T.Fatal(err)
	}
}

// chatRead reads the chat as its block does, the topic picked with its messages, the topics and the turn, into out.
func (e *Env) chatRead(in map[string]any, out any) {
	e.T.Helper()
	all := map[string]json.RawMessage{}
	for _, read := range []string{"deck.chat_topics", "deck.chat_turn", "deck.chat"} {
		var part map[string]json.RawMessage
		if lang, _ := in["lang"].(string); read == "deck.chat_topics" {
			e.Must(read, map[string]any{"lang": lang}, &part)
		} else {
			e.Must(read, in, &part)
		}
		maps.Copy(all, part)
	}
	raw, _ := json.Marshal(all)
	if err := json.Unmarshal(raw, out); err != nil {
		e.T.Fatal(err)
	}
}

// chatData is the value of the chat block of the chat screen read with filter, its parts as one.
func chatData(t testing.TB, data map[string]map[string]json.RawMessage) json.RawMessage {
	t.Helper()
	all := map[string]json.RawMessage{}
	for _, id := range []string{"b0-topics", "b0-turn", "b0"} {
		var part map[string]json.RawMessage
		_ = json.Unmarshal(data[id]["value"], &part)
		maps.Copy(all, part)
	}
	raw, _ := json.Marshal(all)
	return raw
}
