package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"deck2/internal/fault"
	"deck2/internal/files"
	"deck2/internal/gateway"
	"deck2/internal/home"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

const (
	maxJSON = 10 << 20
	maxFile = 1 << 30
)

// HTTP turns the HTTP tools of manifests into registry commands. Secrets reach only the request deck sends:
// the journal keeps the call's input, the caller gets the response.
type HTTP struct {
	GW      *gateway.Gateway
	Secrets func(workspace int64, name string) (string, error)
	Home    home.Layout
	Client  *http.Client
	OAuth   *OAuth
	// Resolve finds a file an input points to for a process of a project.
	Resolve func(ctx context.Context, project, process int64, ref string) (files.Ref, error)
	// Health records the health of a tool; SignIn asks the owner to sign in to a tool's service again.
	Health func(ctx context.Context, p db.Project, tool, status, text string)
	SignIn func(ctx context.Context, p db.Project, tool, why string)
}

func NewHTTP(gw *gateway.Gateway, secrets func(int64, string) (string, error), h home.Layout) *HTTP {
	return &HTTP{GW: gw, Secrets: secrets, Home: h, Client: gateway.Pinned}
}

// Command is the registry command of an HTTP tool of project p; it reaches its own host and the domains of p, a
// name only at public addresses unless the domains name the address.
func (h *HTTP) Command(p db.Project, t manifest.HTTPTool, domains []string) *registry.Command {
	b, _ := json.Marshal(map[string]any{"method": t.Method, "url": t.URL, "hosts": t.Hosts, "headers": t.Headers, "body": t.Body, "auth": t.Auth})
	print := gateway.Canonical(b)
	c := registry.Loose(p.Name+"."+t.Name, t.Title, t.Description, t.Class, registry.Everyone, t.Input, h.handler(p, t, domains))
	c.Result, c.ProjectID = t.Result, p.ID
	c.Gate = registry.Gate{Journals: t.Class == registry.External,
		Fingerprint: func(context.Context, int64, int64, json.RawMessage) string { return print },
		Target:      func(_ string, input json.RawMessage) string { return t.Method + " " + target(t, input) },
		ConfirmTTL:  time.Duration(t.ConfirmTTLMinutes) * time.Minute}
	return c
}

func (h *HTTP) handler(p db.Project, t manifest.HTTPTool, domains []string) registry.Handler {
	name := p.Name + "." + t.Name
	return func(ctx context.Context, act registry.Actor, raw json.RawMessage) (any, error) {
		refuse := func(err error) (any, error) {
			if t.Class == registry.External {
				h.GW.Refused(ctx, act, p.ID, name, raw, err)
			}
			return nil, err
		}
		in, err := manifest.DecodeObject(raw)
		if err != nil {
			return refuse(registry.Invalid("input of %s: %v", name, err))
		}
		req, err := h.request(ctx, act, p, t, domains, in)
		if fault.Status(err) == http.StatusBadGateway {
			h.health(ctx, p, name, "error", err.Error())
		}
		if err != nil {
			return refuse(err)
		}
		var intent int64
		if t.Class == registry.External {
			if intent, err = h.GW.Intent(ctx, act, p.ID, name, raw); err != nil {
				return refuse(err)
			}
		}
		resp, reached, err := h.send(ctx, p, req)
		if err != nil {
			var signIn *ErrSignIn
			isSignIn := errors.As(err, &signIn)
			if isSignIn {
				h.signIn(ctx, p, name, signIn.Why)
			} else {
				err = errors.New(untrusted.Redact(err.Error(), req.secrets))
				h.health(ctx, p, name, "error", err.Error())
				err = fmt.Errorf("deck: %s: %v", name, err)
			}
			switch {
			case intent == 0:
			case !reached:
				h.GW.Finish(ctx, intent, "rejected", "", err.Error())
			default:
				h.GW.Lost(ctx, intent)
				return nil, fmt.Errorf("%v; the outcome is unknown and the owner decides whether to repeat it", err)
			}
			return nil, err
		}
		defer resp.Body.Close()
		out, ext, err := h.response(act, t, resp, req.secrets)
		if resp.StatusCode >= 500 {
			h.health(ctx, p, name, "error", err.Error())
		} else {
			h.health(ctx, p, name, "ok", "")
		}
		done(ctx, h.GW, intent, ext, err)
		return out, err
	}
}

func (h *HTTP) health(ctx context.Context, p db.Project, tool, status, text string) {
	if h.Health != nil {
		h.Health(ctx, p, tool, status, text)
	}
}

func (h *HTTP) signIn(ctx context.Context, p db.Project, tool, why string) {
	h.health(ctx, p, tool, "needs_login", why)
	if h.SignIn != nil {
		h.SignIn(ctx, p, tool, why)
	}
}

// segment escapes a value for one path segment, so it cannot climb up the path either.
func segment(v string) string {
	if v == "." || v == ".." {
		return strings.ReplaceAll(v, ".", "%2E")
	}
	return url.PathEscape(v)
}

// fill puts input fields into a body template: a string that is exactly {field} takes the field as it is and is left
// out when the input has no such field; other strings get fields as text.
func fill(v any, in map[string]any) any {
	switch x := v.(type) {
	case string:
		if m := field.FindStringSubmatch(x); m != nil && m[0] == x {
			return in[m[1]]
		}
		return field.ReplaceAllStringFunc(x, func(m string) string { return fmt.Sprint(in[m[1:len(m)-1]]) })
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if f := fill(e, in); f != nil {
				out[k] = f
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, e := range x {
			out = append(out, fill(e, in))
		}
		return out
	}
	return v
}

// response turns an answer into the tool result: JSON by the result schema or text, both without the call's
// secrets, or a binary body saved as a file in the results folder of the calling process.
func (h *HTTP) response(act registry.Actor, t manifest.HTTPTool, resp *http.Response, secrets []string) (any, string, error) {
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	jsonish := ct == "application/json" || strings.HasSuffix(ct, "+json")
	if resp.StatusCode >= 400 || jsonish || strings.HasPrefix(ct, "text/") || resp.ContentLength == 0 {
		b, err := io.ReadAll(io.LimitReader(resp.Body, maxJSON+1))
		if err != nil {
			return nil, "", fmt.Errorf("deck: %s: reading the answer: %v", t.Name, err)
		}
		big := len(b) > maxJSON
		b = []byte(untrusted.Redact(string(b[:min(len(b), maxJSON)]), secrets))
		if resp.StatusCode >= 400 {
			return nil, "", fmt.Errorf("deck: %s answered HTTP %d: %s", t.Name, resp.StatusCode, untrusted.Cut(string(b), 2000))
		}
		if big {
			return nil, "", fmt.Errorf("deck: %s answered more than %d MB of text; deck does not pass on a cut answer", t.Name, maxJSON>>20)
		}
		if !jsonish {
			return map[string]any{"status": resp.StatusCode, "text": string(b)}, "", nil
		}
		v, err := manifest.DecodeValue(b)
		if err != nil {
			return nil, "", fmt.Errorf("deck: %s answered JSON that does not parse: %v", t.Name, err)
		}
		ext := ""
		if o, ok := v.(map[string]any); ok && o["id"] != nil {
			ext = fmt.Sprint(o["id"])
		}
		if len(t.Result) > 0 {
			if err := manifest.CheckValue(t.Result, v); err != nil {
				return v, ext, fmt.Errorf("deck: the answer of %s does not match its result schema: %v; answer: %s", t.Name, err, untrusted.Cut(string(b), 1000))
			}
		}
		return v, ext, nil
	}
	name, n, err := files.Save(h.Home, act.ProcessID, t.Name, ct, resp.Body, maxFile)
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"file": name, "type": ct, "size": n}, "", nil
}
