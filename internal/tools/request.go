package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"deck2/internal/files"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

var field = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

// headerRef finds secrets and input fields of a header in one pass, so a field value is never read as a secret.
var headerRef = regexp.MustCompile(`\$secret\.[A-Za-z0-9_.-]+|\{[A-Za-z0-9_]+\}`)

// outgoing is a call before it is sent: the request without its body, the body, and oauth2 when the tool
// signs in that way.
type outgoing struct {
	req   *http.Request
	body  *body
	oauth *Auth
}

func (h *HTTP) request(ctx context.Context, act registry.Actor, p db.Project, t manifest.HTTPTool, in map[string]any) (*outgoing, error) {
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
	out := &outgoing{}
	switch {
	case len(t.Body) > 0:
		b, err := makeBody(t, in, func(ref string) (files.Ref, error) { return h.Resolve(ctx, p.ID, act.ProcessID, ref) })
		if err != nil {
			return nil, err
		}
		out.body = b
	case t.Method == http.MethodPost || t.Method == http.MethodPut || t.Method == http.MethodPatch:
		b, _ := json.Marshal(in)
		out.body = &body{json: b}
	}
	req, err := http.NewRequestWithContext(ctx, t.Method, u, nil)
	if err != nil {
		return nil, registry.Invalid("deck: %s: %v", t.Name, err)
	}
	var serr error
	header := func(s string) string {
		return headerRef.ReplaceAllStringFunc(s, func(m string) string {
			if name, ok := strings.CutPrefix(m, "$secret."); ok {
				v, err := h.Secrets(p.WorkspaceID, name)
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
		a := Auth{Header: "Authorization", Prefix: "Bearer "}
		_ = json.Unmarshal(t.Auth, &a)
		switch a.Type {
		case "oauth2":
			out.oauth = &a
		default:
			v, err := h.Secrets(p.WorkspaceID, a.Secret)
			if err != nil && serr == nil {
				serr = err
			}
			req.Header.Set(a.Header, a.Prefix+strings.TrimSpace(v))
		}
	}
	if serr != nil {
		return nil, serr
	}
	if len(missing) > 0 {
		return nil, registry.Invalid("deck: %s needs input fields %s", t.Name, strings.Join(missing, ", "))
	}
	out.req = req
	return out, nil
}

// send makes a call; an oauth2 tool refused with 401 gets its token refreshed and the call once more.
// reached says a request may have got to the service.
func (h *HTTP) send(ctx context.Context, p db.Project, o *outgoing) (*http.Response, bool, error) {
	answered := false
	for attempt := 0; ; attempt++ {
		token := ""
		var err error
		if o.oauth != nil {
			if token, err = h.OAuth.Access(ctx, p.WorkspaceID, *o.oauth, attempt > 0); err != nil {
				return nil, answered, err
			}
		}
		r, err := o.body.attach(o.req.Clone(ctx))
		if err != nil {
			return nil, answered, err
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := h.Client.Do(r)
		if err != nil {
			return nil, answered || sent(err), err
		}
		answered = true
		if resp.StatusCode != http.StatusUnauthorized || o.oauth == nil {
			return resp, true, nil
		}
		_ = resp.Body.Close()
		if attempt > 0 {
			return nil, true, &ErrSignIn{Why: "the service refused a fresh token"}
		}
	}
}
