package scenarios

import (
	"encoding/json"
	"strings"
)

type topicReply struct {
	Process  int64  `json:"process"`
	State    string `json:"state"`
	Provider string `json:"provider"`
	Error    string `json:"error"`
}

type chatMessage struct {
	ID      int64           `json:"id"`
	Role    string          `json:"role"`
	Text    string          `json:"text"`
	Screen  json.RawMessage `json:"screen"`
	Process int64           `json:"process"`
	Reply   *topicReply     `json:"reply"`
}

type chatTopic struct {
	ID       int64        `json:"id"`
	Title    string       `json:"title"`
	Card     int64        `json:"card"`
	Archived bool         `json:"archived"`
	Last     *chatMessage `json:"last"`
	Reply    *topicReply  `json:"reply"`
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
	e.must("deck.chat_send", in, &out)
	return out.Topic
}

func (e *Env) chatMessages(topic int64) []chatMessage {
	e.T.Helper()
	var out struct {
		Messages []chatMessage `json:"messages"`
	}
	e.must("deck.chat_messages", map[string]any{"topic": topic}, &out)
	return out.Messages
}

// masterSaid waits until the topic has n replies of the master and returns them.
func (e *Env) masterSaid(topic int64, n int) []chatMessage {
	e.T.Helper()
	var said []chatMessage
	e.waitFor("the master's replies", func() bool {
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

// projectsOf lists the projects other than the built-in master into out.
func (e *Env) projectsOf(out any) {
	e.T.Helper()
	e.dropMaster("deck.projects_list", "name", out)
}

// menuOf is the menu without the documents screen of the built-in master.
func (e *Env) menuOf(out any) {
	e.T.Helper()
	e.dropMaster("deck.menu", "project", out)
}

func (e *Env) dropMaster(command, field string, out any) {
	e.T.Helper()
	var all []map[string]json.RawMessage
	e.must(command, nil, &all)
	var kept []map[string]json.RawMessage
	for _, x := range all {
		if string(x[field]) != `"master"` {
			kept = append(kept, x)
		}
	}
	b, _ := json.Marshal(kept)
	if err := json.Unmarshal(b, out); err != nil {
		e.T.Fatal(err)
	}
}
