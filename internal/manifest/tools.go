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
	return errs
}

// fitsTool checks that a tool command's input goes to its tool as it is: every command field is a tool
// field, and every field the tool requires the command requires too.
func fitsTool(c Command, t HTTPTool) error {
	var cmd, tool struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	_ = json.Unmarshal(c.InputSchema(), &cmd)
	if len(t.Input) > 0 {
		_ = json.Unmarshal(t.Input, &tool)
	}
	var errs []error
	for f := range cmd.Properties {
		if _, ok := tool.Properties[f]; !ok {
			errs = append(errs, fmt.Errorf("input field %q is not an input field of tool %q, which gets the command input as it is", f, t.Name))
		}
	}
	for _, f := range tool.Required {
		if !slices.Contains(cmd.Required, f) {
			errs = append(errs, fmt.Errorf("tool %q requires input field %q, so the command must require it too", t.Name, f))
		}
	}
	return errors.Join(errs...)
}
