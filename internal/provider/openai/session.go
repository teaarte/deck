package openai

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"github.com/openai/openai-go/v3"
)

// message is one entry of a session transcript, kept by deck so a session resumes like a CLI session does.
type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

var sessionID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

func (a *Adapter) transcript(session string) (string, error) {
	if !sessionID.MatchString(session) {
		return "", errors.New("openai: bad session id")
	}
	return filepath.Join(a.cfg.Sessions, session+".json"), nil
}

// load reads a stored session; ok is false when there is none.
func load(path string) (msgs []message, ok bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return msgs, true, json.Unmarshal(b, &msgs)
}

// save writes the transcript atomically with mode 600.
func save(path string, msgs []message) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(msgs)
	f, err := os.CreateTemp(filepath.Dir(path), ".session-*")
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(f.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(f.Name())
	}
	return werr
}

func params(msgs []message) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "user":
			out = append(out, openai.UserMessage(m.Content))
		case "tool":
			out = append(out, openai.ToolMessage(m.Content, m.ToolCallID))
		case "assistant":
			as := openai.ChatCompletionAssistantMessageParam{}
			if m.Content != "" {
				as.Content.OfString = openai.String(m.Content)
			}
			for _, c := range m.ToolCalls {
				as.ToolCalls = append(as.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{ID: c.ID,
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{Name: c.Name, Arguments: c.Arguments}}})
			}
			out = append(out, openai.ChatCompletionMessageParamUnion{OfAssistant: &as})
		}
	}
	return out
}
