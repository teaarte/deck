package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
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
	RunsDir string
	Client  *http.Client
}

func NewHTTP(gw *gateway.Gateway, secrets func(int64, string) (string, error), runsDir string) *HTTP {
	c := &http.Client{Timeout: 5 * time.Minute, Transport: &http.Transport{Proxy: nil,
		DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext, ResponseHeaderTimeout: 5 * time.Minute},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 || req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme {
				return http.ErrUseLastResponse
			}
			return nil
		}}
	return &HTTP{GW: gw, Secrets: secrets, RunsDir: runsDir, Client: c}
}

// Command is the registry command of an HTTP tool of project p.
func (h *HTTP) Command(p db.Project, t manifest.HTTPTool) *registry.Command {
	title := t.Title
	if title == "" {
		title = t.Name
	}
	return &registry.Command{Name: p.Name + "." + t.Name, Title: title, Description: t.Description, Class: t.Class,
		Input: t.Input, Result: t.Result, Callers: []string{registry.Owner, registry.Master, registry.Agent}, ProjectID: p.ID,
		Journals: t.Class == registry.External, Handler: h.handler(p, t)}
}

func (h *HTTP) handler(p db.Project, t manifest.HTTPTool) registry.Handler {
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
			return refuse(registry.Invalid("deck: input of %s: %v", name, err))
		}
		req, err := h.request(ctx, p.WorkspaceID, t, in)
		if err != nil {
			return refuse(err)
		}
		var intent int64
		if t.Class == registry.External {
			if intent, err = h.GW.Intent(ctx, act, p.ID, name, raw); err != nil {
				return refuse(err)
			}
		}
		resp, err := h.Client.Do(req)
		if err != nil {
			if intent != 0 && sent(err) {
				h.GW.Lost(ctx, intent)
				return nil, fmt.Errorf("deck: %s: the outcome is unknown and the owner decides whether to repeat it: %v", name, err)
			}
			if intent != 0 {
				h.GW.Finish(ctx, intent, "rejected", "", err.Error())
			}
			return nil, fmt.Errorf("deck: %s: %v", name, err)
		}
		defer resp.Body.Close()
		out, ext, err := h.response(act, t, resp)
		if intent != 0 {
			text := ""
			if err != nil {
				text = err.Error()
			}
			h.GW.Finish(ctx, intent, "done", ext, text)
		}
		return out, err
	}
}

// sent reports whether a failed request may have reached the other side; only a failed dial proves it did not.
func sent(err error) bool {
	var op *net.OpError
	var dns *net.DNSError
	return !(errors.As(err, &op) && op.Op == "dial") && !errors.As(err, &dns)
}

var field = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

// headerRef finds secrets and input fields of a header in one pass, so a field value is never read as a secret.
var headerRef = regexp.MustCompile(`\$secret\.[A-Za-z0-9_.-]+|\{[A-Za-z0-9_]+\}`)

func (h *HTTP) request(ctx context.Context, workspace int64, t manifest.HTTPTool, in map[string]any) (*http.Request, error) {
	var missing []string
	put := func(s string, esc func(string) string) string {
		return field.ReplaceAllStringFunc(s, func(m string) string {
			v, ok := in[m[1:len(m)-1]]
			if !ok {
				missing = append(missing, m)
				return ""
			}
			return esc(fmt.Sprint(v))
		})
	}
	path, query, hasQuery := strings.Cut(t.URL, "?")
	u := put(path, segment)
	if hasQuery {
		u += "?" + put(query, url.QueryEscape)
	}
	var body io.Reader
	switch {
	case len(t.Body) > 0:
		var tmpl any
		d := json.NewDecoder(bytes.NewReader(t.Body))
		d.UseNumber()
		if err := d.Decode(&tmpl); err != nil {
			return nil, registry.Invalid("deck: body template of %s: %v", t.Name, err)
		}
		if o, ok := tmpl.(map[string]any); ok && len(o) == 1 && o["file"] != nil {
			return nil, registry.Invalid("deck: %s sends a file; file bodies are not supported yet", t.Name)
		}
		b, _ := json.Marshal(fill(tmpl, in))
		body = bytes.NewReader(b)
	case t.Method == http.MethodPost || t.Method == http.MethodPut || t.Method == http.MethodPatch:
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, t.Method, u, body)
	if err != nil {
		return nil, registry.Invalid("deck: %s: %v", t.Name, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	var serr error
	header := func(s string) string {
		return headerRef.ReplaceAllStringFunc(s, func(m string) string {
			if name, ok := strings.CutPrefix(m, "$secret."); ok {
				v, err := h.Secrets(workspace, name)
				if err != nil && serr == nil {
					serr = err
				}
				return v
			}
			return put(m, func(s string) string { return s })
		})
	}
	for k, v := range t.Headers {
		v = header(v)
		if strings.ContainsAny(v, "\r\n") {
			return nil, registry.Invalid("deck: header %s of %s would contain a line break", k, t.Name)
		}
		req.Header.Set(k, v)
	}
	if len(t.Auth) > 0 {
		var a struct{ Type, Secret, Header, Prefix string }
		a.Header, a.Prefix = "Authorization", "Bearer "
		_ = json.Unmarshal(t.Auth, &a)
		if a.Type != "secret" {
			return nil, registry.Invalid("deck: %s uses %s sign-in, which is not supported yet", t.Name, a.Type)
		}
		v, err := h.Secrets(workspace, a.Secret)
		if err != nil && serr == nil {
			serr = err
		}
		req.Header.Set(a.Header, a.Prefix+strings.TrimSpace(v))
	}
	if serr != nil {
		return nil, serr
	}
	if len(missing) > 0 {
		return nil, registry.Invalid("deck: %s needs input fields %s", t.Name, strings.Join(missing, ", "))
	}
	return req, nil
}

// segment escapes a value for one path segment, so it cannot climb up the path either.
func segment(v string) string {
	if v == "." || v == ".." {
		return strings.ReplaceAll(v, ".", "%2E")
	}
	return url.PathEscape(v)
}

// fill puts input fields into a body template: a string that is exactly {field} takes the field as it is
// and is left out when the input has no such field; other strings get fields as text.
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

// response turns an answer into the tool result: JSON checked by the result schema, text, or a binary body
// saved as a file in the results folder of the calling process.
func (h *HTTP) response(act registry.Actor, t manifest.HTTPTool, resp *http.Response) (any, string, error) {
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	jsonish := ct == "application/json" || strings.HasSuffix(ct, "+json")
	if resp.StatusCode >= 400 || jsonish || strings.HasPrefix(ct, "text/") || resp.ContentLength == 0 {
		b, err := io.ReadAll(io.LimitReader(resp.Body, maxJSON))
		if err != nil {
			return nil, "", fmt.Errorf("deck: %s: reading the answer: %v", t.Name, err)
		}
		if resp.StatusCode >= 400 {
			return nil, "", fmt.Errorf("deck: %s answered HTTP %d: %s", t.Name, resp.StatusCode, truncate(string(b), 2000))
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
				return v, ext, fmt.Errorf("deck: the answer of %s does not match its result schema: %v; answer: %s", t.Name, err, truncate(string(b), 1000))
			}
		}
		return v, ext, nil
	}
	name, n, err := h.save(act, t.Name, ct, resp.Body)
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"file": name, "type": ct, "size": n}, "", nil
}

// save writes a binary answer into the results folder of the process, never following links out of its folder.
func (h *HTTP) save(act registry.Actor, tool, ct string, body io.Reader) (string, int64, error) {
	if act.ProcessID == 0 {
		return "", 0, fmt.Errorf("deck: %s answered with a file, which only a process with a results folder can take", tool)
	}
	root, err := os.OpenRoot(filepath.Join(h.RunsDir, fmt.Sprint(act.ProcessID)))
	if err != nil {
		return "", 0, fmt.Errorf("deck: %s answered with a file, but process %d has no results folder any more", tool, act.ProcessID)
	}
	defer root.Close()
	ext := ""
	exts, _ := mime.ExtensionsByType(ct)
	for _, e := range exts {
		t, _, _ := mime.ParseMediaType(mime.TypeByExtension(e))
		if ext == "" || t == ct {
			ext = e
		}
		if t == ct {
			break
		}
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	name := tool + "-" + hex.EncodeToString(b) + ext
	f, err := root.OpenFile(filepath.Join("results", name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", 0, fmt.Errorf("deck: saving the file %s answered: %v", tool, err)
	}
	n, err := io.Copy(f, io.LimitReader(body, maxFile))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, fmt.Errorf("deck: saving the file %s answered: %v", tool, err)
	}
	return name, n, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
