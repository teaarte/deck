package feed

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"

	"deck2/internal/access"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// maxVoice is the largest recording deck turns into text.
const maxVoice = 10 << 20

type transcribeIn struct {
	File string `type:"file" desc:"The uploaded recording, at most 10 MB"`
}

// transcribe turns a recording the owner uploaded into text with the model assigned to speech to text; the
// recording goes once it is read.
func (s *Service) transcribe(ctx context.Context, act registry.Actor, in transcribeIn) (any, error) {
	id, _ := strconv.ParseInt(in.File, 10, 64)
	f, err := s.St.Q.GetFile(ctx, id)
	if err != nil {
		return nil, registry.NotFound("no file %s", in.File)
	}
	p, err := access.Of(ctx, s.St.Q, act, f.ProjectID, registry.NotFound("no file %s", in.File))
	if err != nil {
		return nil, err
	}
	if p.Builtin == 0 || f.OwnerKind != "upload" || !strings.HasPrefix(f.Type, "audio/") {
		return nil, registry.Invalid("recording_transcribe takes a recording just uploaded for project master")
	}
	// The recording goes whatever the model says.
	defer func() {
		_ = s.St.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error { return q.DeleteFile(ctx, f.ID) })
	}()
	info, err := os.Stat(s.Home.File(f.ID))
	if err != nil {
		return nil, err
	}
	if info.Size() > maxVoice {
		return nil, registry.Invalid("a recording is at most 10 MB")
	}
	act.ProjectID = p.ID
	input, _ := json.Marshal(map[string]string{"kind": "stt", "file": in.File})
	out, err := s.Invoke(ctx, act, registry.Deck("model_call"), input)
	if err != nil {
		return nil, registry.Invalid("speech to text failed: %v", err)
	}
	var heard struct {
		Text string `json:"text"`
	}
	raw, _ := json.Marshal(out)
	if json.Unmarshal(raw, &heard) != nil {
		return nil, registry.Invalid("speech to text answered without text: %s", raw)
	}
	return map[string]any{"text": strings.TrimSpace(heard.Text)}, nil
}

type secureAddress struct {
	HTTPS string `json:"https,omitempty"`
}

// secureAddress is the https address of deck on the owner's tailnet once deck is on it.
func (s *Service) secureAddress(ctx context.Context, act registry.Actor, _ struct{}) (secureAddress, error) {
	if a := s.Tailnet.Load(); a != nil {
		return secureAddress{HTTPS: *a}, nil
	}
	return secureAddress{}, nil
}

func (s *Service) voiceCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("recording_transcribe", "Speech to text",
			"Turns a recording the owner uploaded into text with the model assigned to speech to text; the recording is not kept.",
			registry.OwnerOnly, s.transcribe),
		registry.Read("secure_address_get", "Secure address",
			"Gives the https address of deck on the owner's tailnet, where a browser lets a page record from the microphone; none while deck is not on one.",
			registry.OwnerOnly, s.secureAddress).On("health"),
	}
}
