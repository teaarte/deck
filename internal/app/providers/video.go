package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"deck2/internal/gateway"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/untrusted"
)

// frames are the first and last frames of a video as the pictures of a request: a data URL each, in order.
func (s *Service) frames(ctx context.Context, act registry.Actor, in modelCallIn) ([]any, error) {
	var out []any
	for _, f := range []struct{ ref, kind string }{{in.First, "first_frame"}, {in.Last, "last_frame"}} {
		if f.ref == "" {
			continue
		}
		ref, err := s.Resolve(ctx, act.ProjectID, act.ProcessID, f.ref)
		if err != nil {
			return nil, err
		}
		r, n, err := ref.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(r, min(n, 32<<20)))
		_ = r.Close()
		if err != nil {
			return nil, err
		}
		url := "data:" + http.DetectContentType(b) + ";base64," + base64.StdEncoding.EncodeToString(b)
		out = append(out, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}, "frame_type": f.kind})
	}
	return out, nil
}

// videoJob is a job of a video model as OpenAI and OpenRouter answer it: its id, its state, and why it failed.
type videoJob struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  any    `json:"error"`
}

// video follows the job a video model took until it ends and keeps its clip; done gets what the job cost: what
// its last answer reported, nothing for a failed one that reported nothing, the price of its model past its ceiling.
func (s *Service) video(ctx context.Context, act registry.Actor, h gateway.ModelHop, resp *http.Response, done func(*provider.Usage)) (any, error) {
	_, raw, err := s.answer(act, RoleVideo, resp, h)
	if err != nil {
		done(nil)
		return nil, err
	}
	var job videoJob
	if json.Unmarshal(raw, &job) != nil || job.ID == "" || strings.ContainsAny(job.ID, "/?#") {
		done(&provider.Usage{})
		return nil, fmt.Errorf("deck: %s took the video but answered no job id: %s", h.Connection, untrusted.Cut(string(raw), 500))
	}
	spent := &provider.Usage{}
	defer func() { done(spent) }()
	end := time.Now().Add(s.VideoCeiling)
	for job.Status != "completed" {
		if job.Status == "failed" {
			if spent = gateway.UsageOf(raw); spent == nil {
				zero := 0.0
				spent = &provider.Usage{CostUSD: &zero}
			}
			return nil, fmt.Errorf("deck: %s failed the video job %s: %s", h.Connection, job.ID, untrusted.Cut(failure(job.Error), 1000))
		}
		if !time.Now().Before(end) {
			return nil, fmt.Errorf("deck: the video job %s of %s was not ready in %s", job.ID, h.Connection, s.VideoCeiling)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(s.VideoPoll):
		}
		r, err := gateway.SendModel(ctx, h.Target, gateway.ModelCall{Method: http.MethodGet, Path: "videos/" + job.ID})
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		_ = r.Body.Close()
		if gateway.Fallible(r.StatusCode) {
			continue
		}
		if r.StatusCode >= 400 {
			return nil, fmt.Errorf("deck: %s answered HTTP %d about the video job %s: %s", h.Connection, r.StatusCode, job.ID, untrusted.Cut(string(body), 1000))
		}
		var next videoJob
		if json.Unmarshal(body, &next) == nil && next.Status != "" {
			job.Status, job.Error, raw = next.Status, next.Error, body
		}
	}
	if u := gateway.UsageOf(raw); u != nil {
		spent = u
	}
	r, err := gateway.SendModel(ctx, h.Target, gateway.ModelCall{Method: http.MethodGet, Path: "videos/" + job.ID + "/content"})
	if err != nil {
		return nil, fmt.Errorf("deck: the clip of the video job %s of %s: %v", job.ID, h.Connection, err)
	}
	file, _, err := s.answer(act, RoleVideo, r, h)
	if err == nil && file == nil {
		err = fmt.Errorf("deck: %s answered no clip for the video job %s", h.Connection, job.ID)
	}
	return file, err
}

// failure is why a job failed: the message of an error object, or the error as it came.
func failure(e any) string {
	switch v := e.(type) {
	case string:
		return v
	case map[string]any:
		if m, ok := v["message"].(string); ok {
			return m
		}
	}
	b, _ := json.Marshal(e)
	return string(b)
}
