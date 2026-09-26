package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

var placeholder = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

// checkTemplate keeps an HTTP tool on its host and its secrets in headers: input fields may fill the path,
// query, headers and body, never the scheme or host, and $secret.<name> is allowed only in headers.
func checkTemplate(t HTTPTool) []error {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
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
	var kinds map[string]json.RawMessage
	if json.Unmarshal(t.Body, &kinds) == nil && kinds["file"] != nil {
		var file string
		if err := json.Unmarshal(kinds["file"], &file); err != nil || len(kinds) > 1 {
			fail("a file body is {\"file\": \"{field}\"}")
		} else if m := placeholder.FindStringSubmatch(file); m == nil || m[0] != file || !slices.Contains(FileFields(t.Input), m[1]) {
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

// FitsTool checks that a tool command's input goes to its tool as it is: every command field is a tool
// field, and every field the tool requires the command requires too.
func FitsTool(c Command, name string, input json.RawMessage) error {
	var cmd, tool struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	_ = json.Unmarshal(c.InputSchema(), &cmd)
	if len(input) > 0 {
		_ = json.Unmarshal(input, &tool)
	}
	var errs []error
	for _, f := range sortedKeys(cmd.Properties) {
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

// FileFields are the input fields of a schema whose values point to files.
func FileFields(input json.RawMessage) []string {
	var s struct {
		Properties map[string]struct {
			Format string `json:"format"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(input, &s)
	var out []string
	for _, name := range sortedKeys(s.Properties) {
		if s.Properties[name].Format == "file" {
			out = append(out, name)
		}
	}
	return out
}
