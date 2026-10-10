package gateway

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/untrusted"
)

// ModelTarget is the API of a connection: where it is, the family it speaks (openai, google, anthropic, raw) and its key.
type ModelTarget struct {
	URL string
	API string
	Key string
}

// ModelHop is one place a call of a model may go, in the order deck tries them.
type ModelHop struct {
	Target ModelTarget
	// Connection names the target, Name the model in deck's settings; Model is the name the API knows, which
	// replaces an @role the caller asked for.
	Connection, Name string
	Model            string
	// Local is a connection on the owner's hardware; Free is a model that costs nothing.
	Local, Free bool
	// Options are the fields a call of a kind adds to the request of this model, such as language.
	Options map[string]any
	// Done keeps the call: the status the API answered (0 when the request never reached it, -1 when it broke off
	// after) and what it used, nil when it did not say.
	Done func(ctx context.Context, status int, u *provider.Usage)
}

// ModelRoute finds where a call of a model goes for an actor: the model of deck's settings the request names on the
// connection; an @role names the models assigned to it, the fallbacks after the first.
type ModelRoute func(ctx context.Context, act registry.Actor, ask ModelAsk) ([]ModelHop, error)

type routeKey struct{}

// WithRoute is ctx with the hops a call of a model goes through, so the rules of its command route it no second time.
func WithRoute(ctx context.Context, hops []ModelHop) context.Context {
	return context.WithValue(ctx, routeKey{}, hops)
}

// RouteOf is the hops WithRoute gave ctx.
func RouteOf(ctx context.Context) ([]ModelHop, bool) {
	hops, ok := ctx.Value(routeKey{}).([]ModelHop)
	return hops, ok
}

var modelClient = &http.Client{
	Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		ResponseHeaderTimeout: 5 * time.Minute, ForceAttemptHTTP2: true},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// dropped are headers of the caller deck does not pass on: its own keys, cookies and how the body is packed.
var dropped = []string{DeckModel, "Authorization", "X-Api-Key", "X-Goog-Api-Key", "Cookie", "Proxy-Authorization", "Accept-Encoding",
	"Connection", "Keep-Alive", "Te", "Trailer", "Transfer-Encoding", "Upgrade", "Content-Length", "Host"}

// ModelCall is one request of a model API; Retry is the other way to ask the same when the API has no such path.
type ModelCall struct {
	Method, Path, Query string
	Header              http.Header
	Body                []byte
	Retry               *ModelCall
}

// SendModel makes one request under the API of t with deck's key in place of any the caller sent.
func SendModel(ctx context.Context, t ModelTarget, c ModelCall) (*http.Response, error) {
	u, err := url.Parse(t.URL)
	if err != nil {
		return nil, err
	}
	if path := strings.TrimPrefix(c.Path, "/"); path != "" {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/" + path
		u.RawPath = ""
	}
	q, _ := url.ParseQuery(c.Query)
	q.Del("key")
	for k, vs := range u.Query() {
		q[k] = vs
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, cmp.Or(c.Method, http.MethodPost), u.String(), bytes.NewReader(c.Body))
	if err != nil {
		return nil, err
	}
	for k, vs := range c.Header {
		req.Header[k] = vs
	}
	for _, h := range dropped {
		req.Header.Del(h)
	}
	if t.Key != "" {
		switch t.API {
		case "google":
			req.Header.Set("X-Goog-Api-Key", t.Key)
		case "anthropic":
			req.Header.Set("X-Api-Key", t.Key)
			if req.Header.Get("Anthropic-Version") == "" {
				req.Header.Set("Anthropic-Version", "2023-06-01")
			}
		default:
			req.Header.Set("Authorization", "Bearer "+t.Key)
		}
	}
	return modelClient.Do(req)
}

// SendHops makes the call each hop builds, in order, past one unsent, a 429 or a 5xx; the usage of the answer
// reaches the Done of its hop when its body closes.
func SendHops(ctx context.Context, hops []ModelHop, build func(ModelHop) (ModelCall, error)) (*http.Response, ModelHop, error) {
	var last error = registry.Invalid("deck: no model to call")
	for i, h := range hops {
		c, err := build(h)
		if err != nil {
			return nil, h, err
		}
		start := time.Now()
		resp, err := SendModel(ctx, h.Target, c)
		if err == nil && c.Retry != nil && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed) {
			_ = resp.Body.Close()
			resp, err = SendModel(ctx, h.Target, *c.Retry)
		}
		if err != nil {
			last = &registry.Error{Status: http.StatusBadGateway, Text: fmt.Sprintf("deck: %s: %v", h.Connection, err)}
			if Unsent(err) {
				h.Done(ctx, 0, nil)
				continue
			}
			h.Done(ctx, -1, nil)
			return nil, h, last
		}
		if Fallible(resp.StatusCode) && i < len(hops)-1 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
			_ = resp.Body.Close()
			h.Done(ctx, resp.StatusCode, nil)
			last = &registry.Error{Status: http.StatusBadGateway,
				Text: fmt.Sprintf("deck: %s answered %s: %s", h.Connection, resp.Status, untrusted.Cut(strings.TrimSpace(string(raw)), 500))}
			continue
		}
		done, status := context.WithoutCancel(ctx), resp.StatusCode
		resp.Body = metered(resp.Body, start, chars(c.Body), func(u *provider.Usage) { h.Done(done, status, u) })
		return resp, h, nil
	}
	return nil, ModelHop{}, last
}

// Unsent reports whether a failed request never reached the other side: only a failed dial or name lookup proves
// that; a timeout or a cut answer may have been billed.
func Unsent(err error) bool {
	var op *net.OpError
	var dns *net.DNSError
	return errors.As(err, &op) && op.Op == "dial" || errors.As(err, &dns)
}

// Fallible reports whether a hop's answer is an explicit refusal a fallback may take over: a limit or a fault of
// the service, not an answer about the request.
func Fallible(status int) bool { return status == http.StatusTooManyRequests || status >= 500 }

// ServeHops serves one call of a model through the hops and passes the answer on as it comes; replace puts the
// model of each hop in the body.
func ServeHops(w http.ResponseWriter, r *http.Request, path string, body []byte, replace bool, hops []ModelHop) {
	resp, _, err := SendHops(r.Context(), hops, func(h ModelHop) (ModelCall, error) {
		b := body
		if replace {
			b = WithModel(body, h.Model)
		}
		return ModelCall{Method: r.Method, Path: path, Query: r.URL.RawQuery, Header: r.Header, Body: b}, nil
	})
	if err != nil {
		http.Error(w, err.Error(), registry.Status(err))
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		if k != "Content-Length" && k != "Connection" && k != "Transfer-Encoding" {
			w.Header()[k] = vs
		}
	}
	w.WriteHeader(resp.StatusCode)
	flush(w, resp.Body)
}

// flush copies an answer as it comes, so a stream of events reaches the caller event by event.
func flush(w http.ResponseWriter, body io.Reader) {
	f, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if f != nil {
				f.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}
