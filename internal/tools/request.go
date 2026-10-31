package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"deck2/internal/fault"
	"deck2/internal/files"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

var field = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

// headerRef finds secrets and input fields of a header in one pass, so a field value is never read as a secret.
var headerRef = regexp.MustCompile(manifest.SecretRef.String() + `|\{[A-Za-z0-9_]+\}`)

// outgoing is a call before it is sent: the request without its body, the body, and oauth2 when the tool
// signs in that way.
type outgoing struct {
	req   *http.Request
	body  *body
	oauth *manifest.Auth
	// secrets are the values the call carries; the answer reaches the caller without them.
	secrets []string
}

func (h *HTTP) request(ctx context.Context, act registry.Actor, p db.Project, t manifest.HTTPTool, domains []string, in map[string]any) (*outgoing, error) {
	var missing []string
	out := &outgoing{}
	u, err := h.url(p, t, in, out, &missing)
	if err != nil {
		return nil, err
	}
	values, err := jsonValues(t, in)
	if err != nil {
		return nil, err
	}
	switch {
	case len(t.Body) > 0:
		b, err := makeBody(t, in, values, func(ref string) (files.Ref, error) { return h.Resolve(ctx, p.ID, act.ProcessID, ref) })
		if err != nil {
			return nil, err
		}
		out.body = b
	case t.Method == http.MethodPost || t.Method == http.MethodPut || t.Method == http.MethodPatch:
		b, _ := json.Marshal(values)
		out.body = &body{json: b}
	}
	req, err := http.NewRequestWithContext(ctx, t.Method, u, nil)
	if err != nil {
		return nil, registry.Invalid("%s: %v", t.Name, untrusted.Redact(err.Error(), out.secrets))
	}
	if len(out.secrets) > 0 {
		out.secrets = append(out.secrets, req.URL.String())
	}
	if req, err = gateway.Pin(req, append([]string{req.URL.Host}, domains...)); err != nil {
		return nil, fault.Wrap("deck: "+t.Name, err)
	}
	var serr error
	header := func(s string) string {
		return headerRef.ReplaceAllStringFunc(s, func(m string) string {
			if name, ok := strings.CutPrefix(m, manifest.SecretPrefix); ok {
				v, err := h.Secrets(p.WorkspaceID, name)
				if err != nil && serr == nil {
					serr = err
				}
				out.secrets = append(out.secrets, v)
				return v
			}
			return put(m, in, func(s string) string { return s }, &missing)
		})
	}
	for k, v := range t.Headers {
		v = header(v)
		if strings.ContainsAny(v, "\r\n") {
			return nil, registry.Invalid("header %s of %s would contain a line break", k, t.Name)
		}
		req.Header.Set(k, v)
	}
	if len(t.Auth) > 0 {
		a := t.AuthOf()
		switch a.Type {
		case "oauth2":
			out.oauth = &a
		default:
			v, err := h.Secrets(p.WorkspaceID, a.Secret)
			if err != nil && serr == nil {
				serr = err
			}
			out.secrets = append(out.secrets, v)
			req.Header.Set(a.Header, a.Prefix+strings.TrimSpace(v))
		}
	}
	if serr != nil {
		return nil, serr
	}
	if len(missing) > 0 {
		return nil, registry.Invalid("%s needs input fields %s", t.Name, strings.Join(missing, ", "))
	}
	out.req = req
	return out, nil
}

// url is where a call goes: the URL of the tool with the input in its fields, or the URL its secret holds when that
// is https on one of its hosts, with the query of the tool after it.
func (h *HTTP) url(p db.Project, t manifest.HTTPTool, in map[string]any, out *outgoing, missing *[]string) (string, error) {
	name, query, ok := manifest.SecretURL(t)
	if !ok {
		return fillURL(t.URL, in, missing), nil
	}
	v, err := h.Secrets(p.WorkspaceID, name)
	if err != nil {
		return "", err
	}
	v = strings.TrimSpace(v)
	u, err := url.Parse(v)
	out.secrets = append(out.secrets, linkParts(v, u)...)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" ||
		!slices.Contains(t.Hosts, strings.ToLower(u.Hostname())) {
		return "", registry.Refuse("%s: the secret %s holds no https address on the hosts of the tool, %s; the owner sets it again",
			t.Name, name, strings.Join(t.Hosts, ", "))
	}
	if query != "" {
		sep := "?"
		if u.RawQuery != "" {
			sep = "&"
		}
		v += sep + put(query, in, url.QueryEscape, missing)
	}
	return v, nil
}

// linkParts are the forms a service may echo a private link in: whole, its path and query, the parts of its path
// that look like tokens, and each escaped for JSON or a URL.
func linkParts(v string, u *url.URL) []string {
	parts := []string{v}
	if u != nil {
		parts = append(parts, u.Path, u.EscapedPath(), u.RawQuery)
		for _, seg := range strings.Split(u.Path, "/") {
			if len(seg) >= 8 && strings.ContainsAny(seg, "0123456789") {
				parts = append(parts, seg)
			}
		}
	}
	for _, p := range slices.Clone(parts) {
		parts = append(parts, strings.ReplaceAll(p, "/", `\/`), url.QueryEscape(p))
	}
	return slices.DeleteFunc(parts, func(p string) bool { return len(p) < 8 })
}

// send makes a call; an oauth2 tool refused with 401 gets its token refreshed and the call once more.
// reached says a request may have done something: a 401 refusal did not.
func (h *HTTP) send(ctx context.Context, p db.Project, o *outgoing) (*http.Response, bool, error) {
	for attempt := 0; ; attempt++ {
		token := ""
		var err error
		if o.oauth != nil {
			if token, err = h.OAuth.Access(ctx, p.WorkspaceID, *o.oauth, attempt > 0); err != nil {
				return nil, false, err
			}
		}
		r, err := o.body.attach(o.req.Clone(o.req.Context()))
		if err != nil {
			return nil, false, err
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
			o.secrets = append(o.secrets, token)
		}
		resp, err := h.Client.Do(r)
		if err != nil {
			return nil, !gateway.Unsent(err), err
		}
		if resp.StatusCode != http.StatusUnauthorized || o.oauth == nil {
			return resp, true, nil
		}
		_ = resp.Body.Close()
		if attempt > 0 {
			return nil, false, &ErrSignIn{Why: "the service refused a fresh token"}
		}
	}
}

// jsonValues is the input with each field of format json as the value its text holds, for the body of a request.
func jsonValues(t manifest.HTTPTool, in map[string]any) (map[string]any, error) {
	fields := manifest.FieldsOf(t.Input, "json")
	if len(fields) == 0 {
		return in, nil
	}
	out := maps.Clone(in)
	for _, name := range fields {
		text, ok := in[name].(string)
		if !ok {
			continue
		}
		v, err := manifest.DecodeValue([]byte(text))
		if err != nil {
			return nil, registry.Invalid("input field %s of %s is JSON text: %v", name, t.Name, err)
		}
		out[name] = v
	}
	return out, nil
}

// put fills the {fields} of s from in, escaped by esc, and adds the fields in lacks to missing.
func put(s string, in map[string]any, esc func(string) string, missing *[]string) string {
	return field.ReplaceAllStringFunc(s, func(m string) string {
		v, ok := in[m[1:len(m)-1]]
		if !ok {
			*missing = append(*missing, m)
			return ""
		}
		return esc(fmt.Sprint(v))
	})
}

// fillURL puts the values of the input into the {fields} of the URL of a tool, escaped for the path and the query.
func fillURL(raw string, in map[string]any, missing *[]string) string {
	path, query, hasQuery := strings.Cut(raw, "?")
	u := put(path, in, segment, missing)
	if hasQuery {
		u += "?" + put(query, in, url.QueryEscape, missing)
	}
	return u
}

// target is where a call of a tool with input goes, as the owner is asked about it; its URL as written when the input
// does not fill it, and the hosts a URL from a secret may point to.
func target(t manifest.HTTPTool, input json.RawMessage) string {
	if _, _, ok := manifest.SecretURL(t); ok {
		return fillTarget(t, input) + " (" + strings.Join(t.Hosts, ", ") + ")"
	}
	return fillTarget(t, input)
}

func fillTarget(t manifest.HTTPTool, input json.RawMessage) string {
	in, err := manifest.DecodeObject(input)
	if err != nil {
		return t.URL
	}
	var missing []string
	if u := fillURL(t.URL, in, &missing); len(missing) == 0 {
		return u
	}
	return t.URL
}
