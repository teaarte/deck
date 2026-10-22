package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"

	"golang.org/x/net/publicsuffix"

	"deck2/schemas"
)

func (m *Manifest) compile() error {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	m.scripts = map[string]*Script{}
	for i := range m.Scripts {
		s := &m.Scripts[i]
		if m.scripts[s.Name] != nil {
			fail("script %q: duplicate", s.Name)
		}
		m.scripts[s.Name] = s
	}
	m.compileCollections(fail)
	m.compileCommands(fail)
	m.checkTools(fail)
	m.checkTriggers(fail)
	m.checkScreens(fail)
	for _, name := range m.AllowedExternal {
		if strings.HasPrefix(name, schemas.Builtin+".browser_") {
			fail("allowed_external: %q: the browser acts without asking only on the sites in domains.act", name)
		} else if !m.knownAction(name) {
			fail("allowed_external: %q is neither a command nor a tool of this project", name)
		}
	}
	for i, r := range m.Reads {
		if r.Project == "" {
			fail("reads[%d]: project is required", i)
		}
	}
	m.Domains.Read, m.Domains.Act = m.checkDomains("read", m.Domains.Read), m.checkDomains("act", m.Domains.Act)
	return errors.Join(errs...)
}

// checkDomains gives the domains with "*.x" as x, which already takes the subdomains of x; a URL or a public
// suffix is a warning, so a stored manifest still loads and a save refuses it.
func (m *Manifest) checkDomains(kind string, domains []string) []string {
	out := make([]string, 0, len(domains))
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		d = strings.TrimPrefix(d, "*.")
		host := d
		if h, _, err := net.SplitHostPort(d); err == nil {
			host = strings.Trim(h, "[]")
		}
		_, prefixErr := netip.ParsePrefix(d)
		_, ipErr := netip.ParseAddr(strings.Trim(host, "[]"))
		switch {
		case d == "*" || prefixErr == nil || ipErr == nil:
		case strings.ContainsAny(d, "/?#@ ") || host == "":
			m.warnings = append(m.warnings, fmt.Sprintf("domains.%s: %q: a domain is a host such as example.com or host:port, not a URL", kind, d))
		default:
			if suffix, icann := publicsuffix.PublicSuffix(host); suffix == host && (icann || strings.Contains(host, ".")) {
				m.warnings = append(m.warnings, fmt.Sprintf("domains.%s: %q is a public suffix, which would open every site under it; name the site", kind, d))
			}
		}
		out = append(out, d)
	}
	return out
}

func (m *Manifest) compileCollections(fail func(string, ...any)) {
	m.collections = map[string]*Collection{}
	for i := range m.Collections {
		c := &m.Collections[i]
		if m.collections[c.Name] != nil {
			fail("collection %q: duplicate", c.Name)
		}
		m.collections[c.Name] = c
		var err error
		if c.schema, err = compileSchema("collection-"+c.Name, c.Schema); err != nil {
			fail("collection %q schema: %v", c.Name, err)
		}
		c.fields = schemaFields(c.Schema)
		if c.keyProg, err = compileExpr(c.Key, c.fields); err != nil {
			fail("collection %q key: %v", c.Name, err)
		}
		c.keyFields = exprFields(c.Key, c.fields)
		if hasLet(c.Key) {
			fail("collection %q key: variables (let) are not allowed in a key", c.Name)
		}
		if c.Display != "" {
			if c.displayProg, err = compileField(c.Display, c.fields); err != nil {
				fail("collection %q display: %v", c.Name, err)
			}
		}
		for _, f := range c.Immutable {
			if c.fields != nil && !slices.Contains(c.fields, f) {
				fail("collection %q immutable: unknown field %q", c.Name, f)
			}
		}
		if c.Migration != "" && m.scripts[c.Migration] == nil {
			fail("collection %q migration: unknown script %q", c.Name, c.Migration)
		}
		if c.Folder != "" {
			if c.folderProg, err = compileField(c.Folder, c.fields); err != nil {
				fail("collection %q folder: %v", c.Name, err)
			}
		}
		if c.Parent != nil {
			if c.parentProg, err = compileField(c.Parent.Key, c.fields); err != nil {
				fail("collection %q parent key: %v", c.Name, err)
			}
		}
	}
	for _, c := range m.Collections {
		if c.Parent != nil && (m.collections[c.Parent.Collection] == nil || c.Parent.Collection == c.Name) {
			fail("collection %q parent: no other collection %q", c.Name, c.Parent.Collection)
		}
	}
}

func (m *Manifest) compileCommands(fail func(string, ...any)) {
	m.commands = map[string]*Command{}
	for i := range m.Commands {
		c := &m.Commands[i]
		if m.commands[c.Name] != nil {
			fail("command %q: duplicate", c.Name)
		}
		m.commands[c.Name] = c
		if m.NoNetwork && slices.Contains(c.Requires, "local_browser") {
			fail("command %q requires local_browser, but the shells of a no_network project are confined", c.Name)
		}
		var err error
		if len(c.Input) > 0 {
			if slices.Contains(schemaFields(c.Input), RetryKey) {
				fail("command %q input: %q is reserved: deck adds it to every command", c.Name, RetryKey)
			}
			if err := schemas.CheckInput(c.Input); err != nil {
				fail("command %q input: parameters must use App Intents types: %v", c.Name, err)
			} else if c.input, err = compileSchema("input-"+c.Name, c.Input); err != nil {
				fail("command %q input: %v", c.Name, err)
			}
		}
		if len(c.Result) > 0 {
			if c.result, err = compileSchema("result-"+c.Name, c.Result); err != nil {
				fail("command %q result: %v", c.Name, err)
			}
		}
		switch e := c.Executor; e.Type {
		case "script":
			if m.scripts[e.Script] == nil {
				fail("command %q: unknown script %q", c.Name, e.Script)
			}
		case "model":
			checkModel(c, fail, func(format string, a ...any) { m.warnings = append(m.warnings, fmt.Sprintf(format, a...)) })
		case "tool":
			if !m.knownTool(e.Tool) {
				fail("command %q: unknown tool %q", c.Name, e.Tool)
			}
			for _, t := range m.Tools.HTTP {
				if t.Name == e.Tool {
					if err := FitsTool(*c, t.Name, t.Input); err != nil {
						fail("command %q: %v", c.Name, err)
					}
				}
			}
		}
	}
}

func (m *Manifest) checkTools(fail func(string, ...any)) {
	for name, s := range m.Tools.MCP.Servers {
		if strings.ContainsAny(s.URL, "@?#") {
			fail("mcp server %q: url takes no user, query or key; a key belongs in a secret", name)
		}
	}
	seen := map[string]bool{}
	for i := range m.Tools.HTTP {
		t := &m.Tools.HTTP[i]
		if t.Class == "" {
			t.Class = "external"
		}
		if seen[t.Name] {
			fail("http tool %q: duplicate", t.Name)
		}
		seen[t.Name] = true
		if m.commands[t.Name] != nil {
			fail("http tool %q: a command has the same name", t.Name)
		}
		if len(t.Input) > 0 {
			if err := schemas.CheckInput(t.Input); err != nil {
				fail("http tool %q input: parameters must use App Intents types: %v", t.Name, err)
			} else if _, err := compileSchema("tool-input-"+t.Name, t.Input); err != nil {
				fail("http tool %q input: %v", t.Name, err)
			}
		}
		for _, err := range checkTemplate(*t) {
			fail("http tool %q: %v", t.Name, err)
		}
		if len(t.Result) > 0 {
			if _, err := compileSchema("tool-result-"+t.Name, t.Result); err != nil {
				fail("http tool %q result: %v", t.Name, err)
			}
		}
	}
}

// knownTool reports whether a tool reference resolves: an http tool, "<mcp server>.<tool>" or a builtin "deck.<tool>".
func (m *Manifest) knownTool(name string) bool {
	if slices.ContainsFunc(m.Tools.HTTP, func(t HTTPTool) bool { return t.Name == name }) {
		return true
	}
	server, _, ok := strings.Cut(name, ".")
	if !ok {
		return false
	}
	_, declared := m.Tools.MCP.Servers[server]
	return declared || server == schemas.Builtin
}

func (m *Manifest) knownAction(name string) bool {
	return m.commands[name] != nil || m.knownTool(name)
}

// checkAction verifies that a screen button can call its command: the owner may call it and the params
// cover the required input fields and nothing else.
func (m *Manifest) checkAction(a Action) error {
	c := m.commands[a.Command]
	if c == nil {
		return fmt.Errorf("unknown command %q", a.Command)
	}
	if !slices.Contains(c.Callable(), "owner") {
		return fmt.Errorf("command %q cannot be called by the owner", a.Command)
	}
	in := Props(c.InputSchema())
	for _, r := range in.Required {
		if _, ok := a.Params[r]; !ok && r != a.Input && !slices.Contains(a.FromBlock, r) {
			return fmt.Errorf("command %q needs param %q", a.Command, r)
		}
	}
	if _, ok := in.Properties[a.Input]; a.Input != "" && !ok {
		return fmt.Errorf("command %q has no input field %q", a.Command, a.Input)
	}
	for k := range a.Params {
		if _, ok := in.Properties[k]; !ok && k != RetryKey {
			return fmt.Errorf("command %q has no input field %q", a.Command, k)
		}
	}
	for _, k := range a.FromBlock {
		_, fixed := a.Params[k]
		switch _, ok := in.Properties[k]; {
		case !ok:
			return fmt.Errorf("command %q has no input field %q", a.Command, k)
		case fixed:
			return fmt.Errorf("from_block: %q is a param of the button already", k)
		}
	}
	return nil
}

// CheckButton checks a button a caller builds at run time, as notify does: a command of the project the owner may call.
func (m *Manifest) CheckButton(a Action) error { return m.checkAction(a) }

// PropertyOrder lists the properties of an object schema in the order the schema writes them.
func PropertyOrder(schema json.RawMessage) []string {
	var top map[string]json.RawMessage
	if json.Unmarshal(schema, &top) != nil || top["properties"] == nil {
		return []string{}
	}
	dec := json.NewDecoder(bytes.NewReader(top["properties"]))
	out := []string{}
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return out
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return out
		}
		out = append(out, t.(string))
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return out
		}
	}
	return out
}

// checkModel checks that a model command gives its model what its kind needs; a list of files it warns of, so a
// saved module keeps loading.
func checkModel(c *Command, fail, warn func(string, ...any)) {
	e := c.Executor
	in := Props(c.Input)
	if role, ok := strings.CutPrefix(e.Model, "@"); ok && role != e.Kind {
		fail("command %q: a model of kind %s is @%s or a model by name, not %s", c.Name, e.Kind, e.Kind, e.Model)
	}
	files := FieldsOf(c.Input, "file")
	for _, f := range FileFields(c.Input) {
		if (e.Kind == "video" || e.Kind == "stt" || e.Kind == "image") && !slices.Contains(files, f) {
			warn("command %q: a %s model takes one file a field; input field %s is a list of files", c.Name, e.Kind, f)
		}
	}
	switch e.Kind {
	case "text", "image", "music", "video":
		if e.Prompt == "" && e.PromptFile == "" {
			fail("command %q: a %s model needs prompt or prompt_file", c.Name, e.Kind)
		}
		for _, f := range files {
			if e.Kind == "video" && f != "first_frame" && f != "last_frame" {
				fail("command %q: the file fields of a video model are first_frame and last_frame, not %s", c.Name, f)
			}
		}
		if e.Kind == "image" && len(files) > 1 {
			warn("command %q: an image model edits one picture, the one input field of format file; it has %d", c.Name, len(files))
		}
	case "tts":
		if in.Properties["text"].Kind() != "string" || !slices.Contains(in.Required, "text") {
			fail("command %q: a tts model speaks the input field text, a required string", c.Name)
		}
	case "stt":
		if n := len(files); n != 1 {
			fail("command %q: an stt model takes one input field of format file, the recording; it has %d", c.Name, n)
		}
	}
}
