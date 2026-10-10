package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"time"
	"unicode/utf8"

	"deck2/internal/provider"
)

// metered passes an answer of a model API through and calls done with the usage it reports once it is read.
func metered(body io.ReadCloser, start time.Time, sent int64, done func(*provider.Usage)) io.ReadCloser {
	return &meter{body: body, done: func(model string, u *apiUsage) {
		if u == nil && sent == 0 {
			done(nil)
			return
		}
		if u == nil {
			u = &apiUsage{}
		}
		pu := u.usage(model, time.Since(start))
		pu.Chars = sent
		done(&pu)
	}}
}

// UsageOf is the usage an answer of a model API reports, such as the last poll of a job; nil when it says none.
func UsageOf(raw []byte) *provider.Usage {
	var got *provider.Usage
	m := metered(io.NopCloser(bytes.NewReader(raw)), time.Now(), 0, func(u *provider.Usage) { got = u })
	_, _ = io.Copy(io.Discard, m)
	_ = m.Close()
	return got
}

// chars are the characters of the input of a request, such as the text of speech, in OpenAI's input field.
func chars(body []byte) int64 {
	var v struct {
		Input any `json:"input"`
	}
	if json.Unmarshal(body, &v) != nil {
		return 0
	}
	s, _ := v.Input.(string)
	return int64(utf8.RuneCountInString(s))
}

// apiUsage is the usage of one call as the families report it: OpenAI and compatible APIs (with the cost
// OpenRouter adds), Anthropic, and Google.
type apiUsage struct {
	Prompt     int64    `json:"prompt_tokens"`
	Completion int64    `json:"completion_tokens"`
	Cost       *float64 `json:"cost"`
	Seconds    float64  `json:"seconds"`
	Details    struct {
		Cached  int64 `json:"cached_tokens"`
		Written int64 `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
	Input         int64 `json:"input_tokens"`
	Output        int64 `json:"output_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	CacheCreation int64 `json:"cache_creation_input_tokens"`
	PromptCount   int64 `json:"promptTokenCount"`
	Candidates    int64 `json:"candidatesTokenCount"`
	Thoughts      int64 `json:"thoughtsTokenCount"`
	CachedContent int64 `json:"cachedContentTokenCount"`
}

func (u *apiUsage) merge(o *apiUsage) {
	for _, f := range [][2]*int64{{&u.Prompt, &o.Prompt}, {&u.Completion, &o.Completion}, {&u.Details.Cached, &o.Details.Cached},
		{&u.Details.Written, &o.Details.Written}, {&u.Input, &o.Input}, {&u.Output, &o.Output}, {&u.CacheRead, &o.CacheRead},
		{&u.CacheCreation, &o.CacheCreation}, {&u.PromptCount, &o.PromptCount}, {&u.Candidates, &o.Candidates},
		{&u.Thoughts, &o.Thoughts}, {&u.CachedContent, &o.CachedContent}} {
		*f[0] = max(*f[0], *f[1])
	}
	if o.Cost != nil {
		u.Cost = o.Cost
	}
	u.Seconds = max(u.Seconds, o.Seconds)
}

func (u *apiUsage) usage(model string, d time.Duration) provider.Usage {
	out := provider.Usage{Model: model, Duration: d, CostUSD: u.Cost, Seconds: u.Seconds}
	switch {
	case u.Prompt+u.Completion > 0:
		out.Input, out.Output, out.CacheRead, out.CacheCreation = u.Prompt, u.Completion, u.Details.Cached, u.Details.Written
	case u.Input+u.Output > 0:
		out.Input, out.Output, out.CacheRead, out.CacheCreation = u.Input, u.Output, u.CacheRead, u.CacheCreation
	default:
		out.Input, out.Output, out.CacheRead = u.PromptCount, u.Candidates+u.Thoughts, u.CachedContent
	}
	return out
}

// meter passes a model answer through and finds its usage: in the body, or in the events of a stream.
type meter struct {
	body  io.ReadCloser
	line  []byte
	whole []byte
	model string
	u     *apiUsage
	done  func(model string, u *apiUsage)
	shut  bool
}

func (m *meter) Read(p []byte) (int, error) {
	n, err := m.body.Read(p)
	for _, b := range p[:n] {
		if b == '\n' {
			m.scan(m.line)
			m.line = m.line[:0]
		} else if len(m.line) < 1<<20 {
			m.line = append(m.line, b)
		}
	}
	if len(m.whole) < 4<<20 {
		m.whole = append(m.whole, p[:n]...)
	}
	return n, err
}

func (m *meter) scan(line []byte) {
	line = bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(line), []byte("data:")))
	var v struct {
		Model    string    `json:"model"`
		Version  string    `json:"modelVersion"`
		Usage    *apiUsage `json:"usage"`
		Metadata *apiUsage `json:"usageMetadata"`
		Duration float64   `json:"duration"`
		Message  *struct {
			Model string    `json:"model"`
			Usage *apiUsage `json:"usage"`
		} `json:"message"`
	}
	if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &v) != nil {
		return
	}
	for _, name := range []string{v.Model, v.Version} {
		if name != "" {
			m.model = name
		}
	}
	found := []*apiUsage{v.Usage, v.Metadata}
	if v.Duration > 0 {
		found = append(found, &apiUsage{Seconds: v.Duration})
	}
	if v.Message != nil {
		if v.Message.Model != "" {
			m.model = v.Message.Model
		}
		found = append(found, v.Message.Usage)
	}
	for _, u := range found {
		if u == nil {
			continue
		}
		if m.u == nil {
			m.u = &apiUsage{}
		}
		m.u.merge(u)
	}
}

func (m *meter) Close() error {
	if !m.shut {
		m.shut = true
		m.scan(m.line)
		if m.u == nil {
			m.scan(m.whole)
		}
		m.done(m.model, m.u)
	}
	return m.body.Close()
}
