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

// A session is its transcript of standard chat messages, kept by deck so it resumes like a CLI session.
type transcript = []openai.ChatCompletionMessageParamUnion

var sessionID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

func (a *Adapter) transcript(session string) (string, error) {
	if !sessionID.MatchString(session) {
		return "", errors.New("openai: bad session id")
	}
	return filepath.Join(a.cfg.Sessions, session+".json"), nil
}

// load reads a stored session; ok is false when there is none.
func load(path string) (msgs transcript, ok bool, err error) {
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
func save(path string, msgs transcript) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(msgs)
	if err != nil {
		return err
	}
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

const cutOff = "deck: the session was cut off before this call returned, so its outcome is unknown. " +
	"Check what it did before you make it again."

// answered gives every tool call of a session cut off mid-call a reply, since the API refuses tool calls
// without one; the call itself is not made again.
func answered(msgs transcript) transcript {
	got := map[string]bool{}
	for _, m := range msgs {
		if m.OfTool != nil {
			got[m.OfTool.ToolCallID] = true
		}
	}
	for _, m := range msgs {
		if m.OfAssistant == nil {
			continue
		}
		for _, c := range m.OfAssistant.ToolCalls {
			if id := c.GetID(); id != nil && !got[*id] {
				msgs = append(msgs, openai.ToolMessage(cutOff, *id))
			}
		}
	}
	return msgs
}
