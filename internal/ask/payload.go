package ask

import "encoding/json"

// Decode reads the payload of an ask as the type of its kind.
func Decode[T any, P ~string | ~[]byte](payload P) (T, error) {
	var v T
	err := json.Unmarshal([]byte(payload), &v)
	return v, err
}

// Question is the payload of a question: its questions as the asker gave them, each an object with its question.
type Question struct {
	Questions []json.RawMessage `json:"questions"`
}

// Shown is the payload of a question of a block of the chat: the message that shows it, beside its questions.
type Shown struct {
	Message int64 `json:"message"`
}

// SignIn is the payload of a sign-in: the site of deck's browser or the tool whose service the owner signs in to, or
// the provider of an agent that needs its login again, with the answers that go on.
type SignIn struct {
	Site     string   `json:"site,omitempty"`
	Tool     string   `json:"tool,omitempty"`
	Provider bool     `json:"provider,omitempty"`
	Answers  []string `json:"answers,omitempty"`
}

// Headers are the short headers of the questions that have one.
func (q Question) Headers() []string {
	var hs []string
	for _, raw := range q.Questions {
		var one struct {
			Header string `json:"header"`
		}
		if json.Unmarshal(raw, &one) == nil && one.Header != "" {
			hs = append(hs, one.Header)
		}
	}
	return hs
}
