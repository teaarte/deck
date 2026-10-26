package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

var placeholder = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

// secretURL is a URL taken whole from a secret, with a query of input fields after it.
var secretURL = regexp.MustCompile(`^\$secret\.([A-Za-z0-9_.-]+)(?:\?(.*))?$`)

// hostName is a host name of hosts: lower case, no scheme, port or wildcard.
var hostName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// SecretURL is the secret an HTTP tool takes its whole URL from and the query of input fields it adds; false when
// its URL is written out.
func SecretURL(t HTTPTool) (name, query string, ok bool) {
	m := secretURL.FindStringSubmatch(t.URL)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// checkTemplate keeps an HTTP tool on its host and its secrets in headers: input fields may fill the path,
// query, headers and body, never the scheme or host, and $secret.<name> is allowed only in headers or as the whole
// URL with its hosts.
func checkTemplate(t HTTPTool) []error {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if _, query, ok := SecretURL(t); ok {
		if len(t.Hosts) == 0 {
			fail("a url from a secret needs hosts, the names of the hosts it may point to")
		}
		for _, h := range t.Hosts {
			if !hostName.MatchString(h) {
				fail("hosts: %q is not a host name", h)
			}
		}
		if strings.ContainsAny(query, "#$") {
			fail("url: the query after a secret takes only text and {field}")
		}
		t.URL = "https://host.invalid/?" + query
	} else if len(t.Hosts) > 0 {
		fail("hosts go only with a url from a secret, \"$secret.<name>\"")
	}
	i := strings.Index(t.URL, "://")
	rest := ""
	if i > 0 {
		rest = t.URL[i+3:]
	}
	host, _, _ := strings.Cut(rest, "/")
	host, _, _ = strings.Cut(host, "?")
	u, err := url.Parse(placeholder.ReplaceAllString(t.URL, "x"))
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || host == "":
		fail("url must be an absolute http or https URL")
	case strings.ContainsAny(host, "{}@"):
		fail("url: input fields cannot fill the host")
	}
	if secretRef.MatchString(t.URL) || secretRef.Match(t.Body) {
		fail("$secret is allowed only in headers")
	}
	fields := schemaFields(t.Input)
	if fields == nil {
		fields = []string{}
	}
	used := placeholder.FindAllStringSubmatch(t.URL+string(t.Body), -1)
	for _, v := range t.Headers {
		used = append(used, placeholder.FindAllStringSubmatch(v, -1)...)
	}
	for _, m := range used {
		if !slices.Contains(fields, m[1]) {
			fail("{%s} is not an input field", m[1])
		}
	}
	var auth struct{ AuthURL, TokenURL string }
	_ = json.Unmarshal(t.Auth, &struct {
		A *string `json:"auth_url"`
		T *string `json:"token_url"`
	}{&auth.AuthURL, &auth.TokenURL})
	for _, raw := range []string{auth.AuthURL, auth.TokenURL} {
		if u, err := url.Parse(raw); raw != "" && (err != nil || u.Scheme != "https" && !(u.Scheme == "http" && loopback(u.Hostname()))) {
			fail("oauth2 addresses must be https: %q", raw)
		}
	}
	var kinds map[string]json.RawMessage
	if json.Unmarshal(t.Body, &kinds) == nil && kinds["file"] != nil {
		var file string
		if err := json.Unmarshal(kinds["file"], &file); err != nil || len(kinds) > 1 {
			fail("a file body is {\"file\": \"{field}\"}")
		} else if m := placeholder.FindStringSubmatch(file); m == nil || m[0] != file || !slices.Contains(FieldsOf(t.Input, "file"), m[1]) {
			fail("a file body takes exactly one input field of format file")
		}
	}
	if json.Unmarshal(t.Body, &kinds) == nil && kinds["multipart"] != nil {
		var parts map[string]string
		if len(kinds) > 1 || json.Unmarshal(kinds["multipart"], &parts) != nil {
			fail("a multipart body is {\"multipart\": {\"<part>\": \"text or {field}\"}}")
		}
	}
	return errs
}

func loopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || ip != nil && ip.IsLoopback()
}

// FitsTool checks that a tool command's input goes to its tool as it is: every command field is a tool
// field, and every field the tool requires the command requires too.
func FitsTool(c Command, name string, input json.RawMessage) error {
	cmd, tool := Props(c.InputSchema()), Props(input)
	var errs []error
	for _, f := range slices.Sorted(maps.Keys(cmd.Properties)) {
		if _, ok := tool.Properties[f]; !ok {
			errs = append(errs, fmt.Errorf("input field %q is not an input field of tool %q, which gets the command input as it is", f, name))
		}
	}
	for _, f := range tool.Required {
		if !slices.Contains(cmd.Required, f) {
			errs = append(errs, fmt.Errorf("tool %q requires input field %q, so the command must require it too", name, f))
		}
	}
	return errors.Join(errs...)
}

// FileFields are the input fields of a schema whose values point to files, one or a list, sorted.
func FileFields(input json.RawMessage) []string {
	out := FieldsOf(input, "file")
	for name, p := range Props(input).Properties {
		if p.Items != nil && p.Items.Format == "file" && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// FieldsOf are the input fields of a format, such as json: text with JSON a tool sends on as the value itself.
func FieldsOf(input json.RawMessage, format string) []string {
	props := Props(input).Properties
	var out []string
	for _, name := range slices.Sorted(maps.Keys(props)) {
		if props[name].Format == format {
			out = append(out, name)
		}
	}
	return out
}
