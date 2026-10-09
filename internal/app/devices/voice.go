package devices

import (
	"context"
	"deck2/internal/app/access"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
func (dv *Service) transcribe(ctx context.Context, act registry.Actor, in transcribeIn) (any, error) {
	id, _ := strconv.ParseInt(in.File, 10, 64)
	f, err := dv.St.Q.GetFile(ctx, id)
	if err != nil {
		return nil, registry.NotFound("deck: no file %s", in.File)
	}
	p, err := access.Of(ctx, dv.St.Q, act, f.ProjectID, registry.NotFound("deck: no file %s", in.File))
	if err != nil {
		return nil, err
	}
	if p.Builtin == 0 || f.OwnerKind != "upload" || !strings.HasPrefix(f.Type, "audio/") {
		return nil, registry.Invalid("deck: transcribe takes a recording just uploaded for project master")
	}
	// The recording goes whatever the model says.
	defer func() {
		_ = dv.St.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error { return q.DeleteFile(ctx, f.ID) })
		dv.DrainFiles(context.WithoutCancel(ctx))
	}()
	info, err := os.Stat(filepath.Join(dv.Home.Files(), strconv.FormatInt(f.ID, 10)))
	if err != nil {
		return nil, err
	}
	if info.Size() > maxVoice {
		return nil, registry.Invalid("deck: a recording is at most 10 MB")
	}
	act.ProjectID = p.ID
	input, _ := json.Marshal(map[string]string{"kind": "stt", "file": in.File})
	out, err := dv.Invoke(ctx, act, registry.Builtin+".model_call", input)
	if err != nil {
		return nil, registry.Invalid("deck: speech to text failed: %v", err)
	}
	var heard struct {
		Text string `json:"text"`
	}
	raw, _ := json.Marshal(out)
	if json.Unmarshal(raw, &heard) != nil {
		return nil, registry.Invalid("deck: speech to text answered without text: %s", raw)
	}
	return map[string]any{"text": strings.TrimSpace(heard.Text)}, nil
}

// VoiceCommands are the commands of speech to text.
func (dv *Service) VoiceCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("transcribe", "Speech to text",
			"Turns a recording the owner uploaded into text with the model assigned to speech to text; the recording is not kept.",
			registry.OwnerOnly, dv.transcribe),
	}
}
