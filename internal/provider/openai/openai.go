// Package openai runs a model behind an OpenAI-compatible chat API in deck's own thin tool loop: the tools
// are those of the deck gateway, the session is a transcript deck keeps.
package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"deck2/internal/provider"
)

type Config struct {
	Name  string
	URL   string
	Model string
	// NoCloud means the model runs on the owner's own hardware.
	NoCloud bool
	// Key gives the API key at each call; empty sends none.
	Key func() (string, error)
	// Sessions is the folder of session transcripts, outside every folder the agent writes to.
	Sessions string
	// HTTP is the client of the API; http.DefaultClient when nil.
	HTTP *http.Client
	// Now is the deck clock, for the reset time of a usage limit.
	Now func() time.Time
}

type Adapter struct{ cfg Config }

func New(cfg Config) *Adapter {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Adapter{cfg: cfg}
}

func (a *Adapter) Name() string { return a.cfg.Name }

// Endpoint is the base URL and the model, for the list of providers.
func (a *Adapter) Endpoint() (string, string) { return a.cfg.URL, a.cfg.Model }

// Caps: no sandbox or shell of its own, so commands that need them do not run here.
func (a *Adapter) Caps() provider.Caps {
	c := provider.Caps{provider.Tools, provider.Resume, provider.Intercept}
	if a.cfg.NoCloud {
		c = append(c, provider.NoCloud)
	}
	return c
}

func (a *Adapter) QuestionTool() string { return provider.AskTool }

// service builds the chat client from deck's own settings only, never from OPENAI_* variables of deck's
// environment; it does not retry, so a usage limit reaches the pause of deck at once.
func (a *Adapter) service() (openai.ChatCompletionService, error) {
	opts := []option.RequestOption{option.WithBaseURL(strings.TrimSuffix(a.cfg.URL, "/") + "/"), option.WithMaxRetries(0)}
	if a.cfg.HTTP != nil {
		opts = append(opts, option.WithHTTPClient(a.cfg.HTTP))
	} else {
		opts = append(opts, option.WithHTTPClient(http.DefaultClient))
	}
	if a.cfg.Key != nil {
		key, err := a.cfg.Key()
		if err != nil {
			return openai.ChatCompletionService{}, err
		}
		if key != "" {
			opts = append(opts, option.WithAPIKey(key))
		}
	}
	return openai.NewChatCompletionService(opts...), nil
}

func (a *Adapter) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	var res provider.Result
	path, err := a.transcript(req.SessionID)
	if err != nil {
		return res, err
	}
	msgs, found, err := load(path)
	if err != nil {
		return res, fmt.Errorf("openai: read the session: %w", err)
	}
	if req.Resume && !found {
		res.SessionMissing = true
		return res, nil
	}
	if !req.Resume {
		msgs = nil
	}
	msgs = append(spoken(answered(msgs)), openai.UserMessage(req.Prompt))
	if err := save(path, msgs); err != nil {
		return res, err
	}
	chat, err := a.service()
	if err != nil {
		return res, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "deck-openai", Version: "0"}, nil)
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: req.MCPURL}, nil)
	if err != nil {
		return res, fmt.Errorf("openai: connect to the deck gateway: %w", err)
	}
	defer sess.Close()
	permission := req.PermissionMCP[strings.LastIndex(req.PermissionMCP, "__")+2:]
	tools, err := gatewayTools(ctx, sess, permission)
	if err != nil {
		return res, fmt.Errorf("openai: list the deck tools: %w", err)
	}
	for {
		start := time.Now()
		resp, err := chat.New(ctx, openai.ChatCompletionNewParams{Model: a.cfg.Model, Messages: msgs, Tools: tools})
		if err != nil {
			return refused(res, err, a.cfg.Now())
		}
		if req.OnUsage != nil {
			u := resp.Usage
			req.OnUsage(provider.Usage{Model: resp.Model, RequestModel: a.cfg.Model, Input: u.PromptTokens, Output: u.CompletionTokens,
				CacheRead: u.PromptTokensDetails.CachedTokens, CacheCreation: u.PromptTokensDetails.CacheWriteTokens,
				Duration: time.Since(start)})
		}
		if len(resp.Choices) == 0 {
			return res, errors.New("openai: the reply had no choices")
		}
		reply := resp.Choices[0].Message
		msgs = append(msgs, reply.ToParam())
		if err := save(path, msgs); err != nil {
			return res, err
		}
		if reply.Content != "" {
			emit(req, provider.Event{Kind: provider.EventMessage, Text: reply.Content})
		}
		if len(reply.ToolCalls) == 0 {
			res.Text = reply.Content
			return res, nil
		}
		for _, c := range reply.ToolCalls {
			emit(req, provider.Event{Kind: provider.EventTool, ID: c.ID, Title: c.Function.Name, Text: c.Function.Arguments})
			out, ok := call(ctx, sess, c.Function.Name, c.Function.Arguments)
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			emit(req, provider.Event{Kind: provider.EventToolUpdate, ID: c.ID, Status: provider.ToolStatus(ok), Text: out})
			msgs = append(msgs, openai.ToolMessage(out, c.ID))
			if err := save(path, msgs); err != nil {
				return res, err
			}
		}
	}
}

func emit(req provider.Request, e provider.Event) {
	if req.OnEvent != nil {
		req.OnEvent(e)
	}
}

// refused turns an API error into the outcome of the turn: a usage limit (429) pauses, a refused key needs a
// login, the rest is an error with its text.
func refused(res provider.Result, err error, now time.Time) (provider.Result, error) {
	var api *openai.Error
	if !errors.As(err, &api) || api.Response == nil {
		return res, fmt.Errorf("openai: %w", err)
	}
	res.IsError, res.Text = true, api.Message
	if res.Text == "" {
		res.Text = http.StatusText(api.StatusCode)
	}
	switch api.StatusCode {
	case http.StatusTooManyRequests:
		res.RateLimited = true
		after := api.Response.Header.Get("Retry-After")
		if s, err := strconv.Atoi(after); err == nil && s > 0 {
			res.ResetsAt = now.Add(time.Duration(s) * time.Second).Unix()
		} else if t, err := http.ParseTime(after); err == nil {
			res.ResetsAt = t.Unix()
		}
		return res, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		res.NeedsLogin = true
		return res, nil
	}
	return res, fmt.Errorf("openai: %d %s", api.StatusCode, res.Text)
}
