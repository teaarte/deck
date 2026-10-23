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
	"sync"
	"sync/atomic"
	"time"

	"deck2/internal/fault"
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
	// TPM and Context are the tokens a minute and of one request the model takes, 0 when not known.
	TPM, Context int64
	// Local is a connection on the owner's hardware; Free is a model that costs nothing; Queue sends one call at a
	// time to its server, such as a local server of pictures that would time out the calls it holds.
	Local, Free, Queue bool
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

// route is what the proxy read of a call of a model and the hops it goes through.
type route struct {
	ask  ModelAsk
	hops []ModelHop
}

// WithRoute is ctx with what a call of a model asks and the hops it goes through, so the rules of its command read
// and route it no second time.
func WithRoute(ctx context.Context, ask ModelAsk, hops []ModelHop) context.Context {
	return context.WithValue(ctx, routeKey{}, route{ask, hops})
}

// RouteOf is what WithRoute gave ctx.
func RouteOf(ctx context.Context) (ModelAsk, []ModelHop, bool) {
	r, ok := ctx.Value(routeKey{}).(route)
	return r.ask, r.hops, ok
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
		if past := h.past(c.Body); past != "" {
			last = &registry.Error{Status: http.StatusTooManyRequests, Text: past}
			continue
		}
		release, waited, err := queued(ctx, h)
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
			release()
			text := fmt.Sprintf("deck: %s: %v", h.Connection, err)
			if h.Queue {
				text = fmt.Sprintf("deck: the local model %s did not answer in %s (%d calls waited for it before): %v", h.Name, time.Since(start).Round(time.Second), waited, err)
			}
			last = &registry.Error{Status: http.StatusBadGateway, Text: text}
			if Unsent(err) {
				h.Done(ctx, 0, nil)
				continue
			}
			h.Done(ctx, -1, nil)
			return nil, h, last
		}
		if past := pastMinute(resp); past != "" {
			// A request past what the model takes a minute: the next model takes it, and the model waits for nothing.
			release()
			h.Done(ctx, resp.StatusCode, nil)
			last = &registry.Error{Status: http.StatusTooManyRequests, Text: fmt.Sprintf("deck: %s answered %s: %s", h.Connection, resp.Status, past)}
			continue
		}
		if Fallible(resp.StatusCode) && i < len(hops)-1 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
			_ = resp.Body.Close()
			release()
			h.Done(ctx, resp.StatusCode, nil)
			last = &registry.Error{Status: http.StatusBadGateway,
				Text: fmt.Sprintf("deck: %s answered %s: %s", h.Connection, resp.Status, untrusted.Cut(strings.TrimSpace(string(raw)), 500))}
			continue
		}
		done, status := context.WithoutCancel(ctx), resp.StatusCode
		resp.Body = &releasing{metered(resp.Body, start, chars(c.Body), func(u *provider.Usage) { h.Done(done, status, u) }), release}
		return resp, h, nil
	}
	return nil, ModelHop{}, last
}

// bytesPerToken is how deck guesses the tokens of a request before it goes: four bytes a token.
const bytesPerToken = 4

// past says why a request of body is past a limit of the model of h, empty when it is not or the model has none.
func (h ModelHop) past(body []byte) string {
	n := int64(len(body) / bytesPerToken)
	switch {
	case h.Context > 0 && n > h.Context:
		return fmt.Sprintf("deck: the request of about %d tokens is past the %d tokens a request of %s holds", n, h.Context, h.Name)
	case h.TPM > 0 && n > h.TPM:
		return fmt.Sprintf("deck: the request of about %d tokens is past the %d tokens a minute %s takes", n, h.TPM, h.Name)
	}
	return ""
}

// pastMinute is the words of a 413 of a model API past its limit per minute, which it closes; any other answer it
// leaves whole, a 413 past another rate as a 429.
func pastMinute(resp *http.Response) string {
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		return ""
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if st := fault.ModelAnswer(resp.StatusCode, raw); st != fault.PastMinute {
		resp.StatusCode = st
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(raw), resp.Body), resp.Body}
		return ""
	}
	_ = resp.Body.Close()
	return untrusted.Cut(strings.TrimSpace(string(raw)), 500)
}

// queues hold the one call at a time of a queued hop, by the server of its connection.
var queues sync.Map

type queue struct {
	slot    chan struct{}
	waiting atomic.Int64
}

// queued waits for the turn of a queued hop at its server; release ends it, waited is how many calls were ahead.
func queued(ctx context.Context, h ModelHop) (release func(), waited int64, err error) {
	if !h.Queue {
		return func() {}, 0, nil
	}
	server := h.Target.URL
	if u, err := url.Parse(h.Target.URL); err == nil && u.Host != "" {
		server = u.Host
	}
	v, _ := queues.LoadOrStore(server, &queue{slot: make(chan struct{}, 1)})
	q := v.(*queue)
	waited = q.waiting.Add(1) - 1 + int64(len(q.slot))
	defer q.waiting.Add(-1)
	select {
	case q.slot <- struct{}{}:
		return sync.OnceFunc(func() { <-q.slot }), waited, nil
	case <-ctx.Done():
		return nil, waited, fmt.Errorf("deck: the call waited for the local model %s behind %d others until it was cut: %w", h.Name, waited, context.Cause(ctx))
	}
}

// releasing ends the turn of a queued hop when its answer closes.
type releasing struct {
	io.ReadCloser
	release func()
}

func (r *releasing) Close() error {
	err := r.ReadCloser.Close()
	r.release()
	return err
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
func Fallible(status int) bool {
	t := fault.OfStatus(fault.ModelStatus(status), true)
	return t == fault.ModelLimit || t == fault.ModelUnavailable
}

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
		if registry.Status(err) == http.StatusTooManyRequests {
			w.Header().Set(provider.PastLimit, "1")
		}
		http.Error(w, err.Error(), registry.Status(err))
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		if k != "Content-Length" && k != "Connection" && k != "Transfer-Encoding" {
			w.Header()[k] = vs
		}
	}
	w.WriteHeader(fault.ModelStatus(resp.StatusCode))
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
