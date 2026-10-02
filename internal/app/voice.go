package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"deck2/internal/health"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

const (
	settingSTT = "setting:stt"
	// DefaultSTT is the whisper.cpp server on this machine.
	DefaultSTT = "http://127.0.0.1:8178/inference"
	// maxVoice is the largest recording deck turns into text.
	maxVoice  = 10 << 20
	sttHealth = "stt"
)

// sttDef is the speech-to-text provider of a workspace: an OpenAI-compatible transcription endpoint.
type sttDef struct {
	URL       string `json:"url"`
	Model     string `json:"model,omitempty"`
	KeySecret string `json:"key_secret,omitempty"`
	Language  string `json:"language,omitempty"`
}

func (a *App) stt(ctx context.Context, workspace int64) sttDef {
	d := sttDef{URL: a.Cfg.STT}
	if raw, err := a.Store.Q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: workspace, Key: settingSTT}); err == nil {
		_ = json.Unmarshal([]byte(raw), &d)
	}
	if d.URL == "" {
		d.URL = DefaultSTT
	}
	return d
}

func (a *App) sttSet(ctx context.Context, act registry.Actor, in struct {
	URL       string `json:"url"`
	Model     string `json:"model"`
	KeySecret string `json:"key_secret"`
	Language  string `json:"language"`
}) (any, error) {
	d := sttDef{URL: strings.TrimSpace(in.URL), Model: in.Model, KeySecret: in.KeySecret, Language: in.Language}
	switch {
	case !strings.HasPrefix(d.URL, "http://") && !strings.HasPrefix(d.URL, "https://"):
		return nil, registry.Invalid("deck: the speech-to-text url is http or https, such as %s", DefaultSTT)
	case strings.ContainsAny(d.URL, "@?#"):
		return nil, registry.Invalid("deck: the url takes no user, query or key; keep the key in a secret and name it in key_secret")
	case d.KeySecret != "" && !validSecret(d.KeySecret):
		return nil, registry.Invalid("deck: bad secret name %q", d.KeySecret)
	}
	raw, _ := json.Marshal(d)
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.SetSetting(ctx, db.SetSettingParams{WorkspaceID: act.Workspace, Key: settingSTT, Value: string(raw)})
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("settings")
	return d, nil
}

// transcribe turns a recording the owner uploaded into text with the speech-to-text provider; the recording
// goes once it is read.
func (a *App) transcribe(ctx context.Context, act registry.Actor, in struct{ File string }) (any, error) {
	id, _ := strconv.ParseInt(in.File, 10, 64)
	f, err := a.Store.Q.GetFile(ctx, id)
	if err != nil {
		return nil, registry.NotFound("deck: no file %s", in.File)
	}
	p, err := projectOf(ctx, a.Store.Q, act, f.ProjectID, registry.NotFound("deck: no file %s", in.File))
	if err != nil {
		return nil, err
	}
	if p.Builtin == 0 || f.OwnerKind != "upload" || !strings.HasPrefix(f.Type, "audio/") {
		return nil, registry.Invalid("deck: transcribe takes a recording just uploaded for project master")
	}
	// The recording goes whatever the provider says.
	defer func() {
		_ = a.Store.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error { return q.DeleteFile(ctx, f.ID) })
		a.removeFiles([]int64{f.ID})
	}()
	path := filepath.Join(a.dirs().Files, strconv.FormatInt(f.ID, 10))
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxVoice {
		return nil, registry.Invalid("deck: a recording is at most 10 MB")
	}
	audio, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	d := a.stt(ctx, act.Workspace)
	text, err := a.whisper(ctx, act.Workspace, d, f.Name, audio)
	a.setSTTHealth(ctx, err)
	if err != nil {
		return nil, registry.Invalid("deck: speech to text at %s failed: %v", d.URL, err)
	}
	return map[string]any{"text": text}, nil
}

var sttClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// whisper posts a recording as a multipart form the way OpenAI's transcriptions and whisper.cpp's server take it.
func (a *App) whisper(ctx context.Context, workspace int64, d sttDef, name string, audio []byte) (string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", filepath.Base(name))
	if err != nil {
		return "", err
	}
	_, _ = part.Write(audio)
	_ = w.WriteField("response_format", "json")
	if d.Model != "" {
		_ = w.WriteField("model", d.Model)
	}
	if d.Language != "" {
		_ = w.WriteField("language", d.Language)
	}
	_ = w.Close()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if d.KeySecret != "" {
		key, err := a.Secret(workspace, d.KeySecret)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := sttClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", resp.Status, untrusted.Cut(strings.TrimSpace(string(raw)), 300))
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("the answer is not JSON with text: %s", untrusted.Cut(string(raw), 200))
	}
	return strings.TrimSpace(out.Text), nil
}

func (a *App) setSTTHealth(ctx context.Context, failed error) {
	status, text := "ok", ""
	if failed != nil {
		status, text = "error", failed.Error()
	}
	health.Write(ctx, a.Store, a.Bus, sttHealth, status, text)
}
