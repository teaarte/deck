package manifest

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"golang.org/x/net/publicsuffix"

	"deck2/internal/registry"
	"deck2/schemas"
)

// The answers of the owner a grant keeps: always, until revoked or the print changes; no, which the module
// remembers and does not ask again.
const (
	Always = "always"
	No     = "no"
)

// Grant is an answer of the owner to a right of a module on one subject: the print of the subject it was given to,
// empty for a subject that has none.
type Grant struct {
	Right   string
	Who     string
	Subject string
	Answer  string
	Print   string
}

// Grants are the answers of the owner to the rights of one module.
type Grants []Grant

// gives says the grant answers always to need on subject, the print it holds to being the need's.
func (g Grant) gives(n registry.Need, subject string) bool {
	return g.Answer == Always && g.Right == n.Right && g.Who == whom(n.Right, n.Who) && g.Subject == subject &&
		(g.Print == "" || g.Print == n.Print)
}

// Declares says the module declared the right of need on a subject that covers its subject.
func (m *Manifest) Declares(n registry.Need) bool {
	for _, d := range m.Rights {
		if d.Right == n.Right && d.Whom() == whom(n.Right, n.Who) && slices.ContainsFunc(d.What, func(s string) bool { return Covers(n.Right, s, n.Subject) }) {
			return true
		}
	}
	return false
}

// Names says the module declared the subject of need by name, not as any: an injection does not choose where it goes.
func (m *Manifest) Names(n registry.Need) bool {
	for _, d := range m.Rights {
		if d.Right == n.Right && d.Whom() == whom(n.Right, n.Who) && slices.ContainsFunc(d.What, func(s string) bool { return s != "*" && Covers(n.Right, s, n.Subject) }) {
			return true
		}
	}
	return false
}

// Holds says the module holds need: a declared subject that covers it the owner answered always, to its print.
func (m *Manifest) Holds(g Grants, n registry.Need) bool {
	for _, d := range m.Rights {
		if d.Right != n.Right || d.Whom() != whom(n.Right, n.Who) {
			continue
		}
		for _, s := range d.What {
			if Covers(n.Right, s, n.Subject) && slices.ContainsFunc(g, func(x Grant) bool { return x.gives(n, s) }) {
				return true
			}
		}
	}
	return false
}

// Denies says the owner answered no to need on its subject: the module does not use it and does not ask.
func (g Grants) Denies(n registry.Need) bool {
	return slices.ContainsFunc(g, func(x Grant) bool {
		return x.Answer == No && x.Right == n.Right && x.Who == whom(n.Right, n.Who) && x.Subject == n.Subject
	})
}

// Gives says the owner answered always to need on its subject itself, declared or not: a command of deck or a site
// of the browser that no manifest names.
func (g Grants) Gives(n registry.Need) bool {
	return slices.ContainsFunc(g, func(x Grant) bool { return x.gives(n, n.Subject) })
}

// Held are the declared subjects of a right whom it opens to the owner answered always, each once.
func (m *Manifest) Held(g Grants, right, who string) []string {
	var out []string
	for _, d := range m.Rights {
		if d.Right != right || d.Whom() != whom(right, who) {
			continue
		}
		for _, s := range d.What {
			if !slices.Contains(out, s) && slices.ContainsFunc(g, func(x Grant) bool { return x.gives(registry.Need{Right: right, Who: who, Subject: s}, s) }) {
				out = append(out, s)
			}
		}
	}
	return out
}

// Covers says a declared subject of a right covers what a call acts on: itself; * of a network any name, a domain its
// subdomains; * of data all, a module its data, *:models and *:runs; * of files folders; * of modules all, a module its commands.
func Covers(right, has, want string) bool {
	if has == want {
		return true
	}
	switch right {
	case Network:
		_, literal := address(want)
		return has == "*" && !literal || !literal && strings.HasSuffix(want, "."+has)
	case Data:
		p, rest, _ := strings.Cut(want, ":")
		return has == "*" || rest == "" && (strings.HasPrefix(want, has+".") || strings.HasPrefix(want, has+"/")) || has == "*:"+rest && p != ""
	case Files:
		return has == "*" && want != GPU
	case Modules:
		return has == "*" || strings.HasPrefix(want, has+".")
	}
	return false
}

// ToolRight is the right an HTTP tool needs and its subject: network on its host for GET or HEAD, send on the tool for
// another method, or the right its author names; a tool with a key reads the owner's data, never only the network.
func ToolRight(t HTTPTool) (right, subject string) {
	keyed := len(toolSecrets(t)) > 0
	right = Send
	if t.Method == "GET" || t.Method == "HEAD" {
		right = Network
	}
	right = cmp.Or(t.Right, right)
	if right == Network && keyed {
		right = Data
	}
	if right != Network {
		return right, t.Name
	}
	return Network, ToolHost(t)
}

// ToolHost is the host an HTTP tool calls, empty for a URL from a secret or one that does not parse.
func ToolHost(t HTTPTool) string {
	u, err := url.Parse(t.URL)
	if err != nil || strings.HasPrefix(t.URL, SecretPrefix) {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// ServerRight is the right a tool of an MCP server needs: the one its manifest names, else send.
func ServerRight(s MCPServer, tool string) string { return cmp.Or(s.Right[tool], Send) }

// ToolPrint is what an HTTP tool sends as it is now, the print an answer of always to it holds to.
func ToolPrint(t HTTPTool) string {
	b, _ := json.Marshal(map[string]any{"method": t.Method, "url": t.URL, "hosts": t.Hosts, "headers": t.Headers, "body": t.Body, "auth": t.Auth})
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return string(b)
	}
	out, _ := json.Marshal(v)
	return string(out)
}

// CommandNeed is the right a call of a command of the module needs: send on it when it declares that, else none.
func (m *Manifest) CommandNeed(name string) (registry.Need, bool) {
	n := registry.Need{Right: Send, Subject: name}
	c := m.Command(name)
	if c == nil || !m.Declares(n) {
		return registry.Need{}, false
	}
	n.Print = c.Executor.Type + "/" + c.Executor.Tool
	return n, true
}

// PrintOf is the print of what a right of the module acts on as its manifest has it, which an answer of always holds
// to: of its HTTP tool or command of that name, empty for any other subject.
func (m *Manifest) PrintOf(n registry.Need) string {
	if n.Right == Network {
		return ""
	}
	for _, t := range m.Tools.HTTP {
		if right, subject := ToolRight(t); t.Name == n.Subject && right == n.Right && subject == n.Subject {
			return ToolPrint(t)
		}
	}
	if c, ok := m.CommandNeed(n.Subject); ok && n.Right == Send {
		return c.Print
	}
	return ""
}

// MayWrite reports whether the agents and scripts of the project may write the document of this name, or every
// document of a folder named with "/" at its end.
func (m *Manifest) MayWrite(name string) bool {
	return slices.ContainsFunc(m.Writes, func(w string) bool {
		return w == name || strings.HasSuffix(w, "/") && strings.HasPrefix(name, w)
	})
}

// Uses are the connections, models and @roles the module declares it spends money through.
func (m *Manifest) Uses() []string {
	var out []string
	for _, d := range m.Rights {
		if d.Right == Money {
			for _, s := range d.What {
				if !slices.Contains(out, s) {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// Called are the uses of Uses but those of its commands of type model: what its scripts and agents call models through.
func (m *Manifest) Called() []string {
	return slices.DeleteFunc(m.Uses(), func(u string) bool {
		return slices.ContainsFunc(m.Commands, func(c Command) bool {
			return c.Executor.Type == "model" && cmp.Or(c.Executor.Model, "@"+c.Executor.Kind) == u
		})
	})
}

// DataOf is what the module declares it reads of the data of other modules: whole projects, collections as
// project.collection, documents as project/name, models and runs as project:models and project:runs.
func (m *Manifest) DataOf() []string {
	var out []string
	for _, d := range m.Rights {
		if d.Right == Data {
			out = append(out, d.What...)
		}
	}
	return out
}

// service says a subject of data names a tool of the module or a tool of its MCP server, a service it reads with
// a key, rather than another module.
func (m *Manifest) service(subject string) bool {
	if slices.ContainsFunc(m.Tools.HTTP, func(t HTTPTool) bool { return t.Name == subject }) {
		return true
	}
	server, _, ok := strings.Cut(subject, ".")
	_, declared := m.Tools.MCP.Servers[server]
	return ok && declared
}

// AtUse says the owner answers need when it is first used, not when enabling the module gives it in advance: a row of
// the catalog whose always takes a fresh passkey, or the network of the shells of agents.
func AtUse(n registry.Need) bool {
	r, ok := RightOf(n.Right)
	return !ok || r.Key || whom(n.Right, n.Who) == Agents
}

// Others are the data subjects of the module that are not its own services: other modules, memory, everything.
func (m *Manifest) Others() []string {
	return slices.DeleteFunc(m.DataOf(), m.service)
}

// checkRights warns of rights outside the catalog, subjects deck cannot read, an agent's network beside a login or
// beside the data of others, a tool or a model command whose right is not declared, and an undo that names nothing.
func (m *Manifest) checkRights() {
	warn := func(format string, a ...any) { m.warnings = append(m.warnings, fmt.Sprintf(format, a...)) }
	var agentNet bool
	for i, d := range m.Rights {
		if _, ok := RightOf(d.Right); !ok {
			warn("rights[%d]: %q is no right of deck's catalog: network, login, data, files, money, send or modules", i, d.Right)
			continue
		}
		if d.Who != "" && d.Right != Network {
			warn("rights[%d]: who is only of network", i)
		}
		agentNet = agentNet || d.Whom() == Agents
		for _, s := range d.What {
			if why := m.badSubject(d.Right, s); why != "" {
				warn("rights[%d]: %q: %s", i, s, why)
			}
		}
	}
	if agentNet && slices.ContainsFunc(m.Rights, func(d Declared) bool { return d.Right == Login }) {
		warn("rights: an agent on a subscription login never gets the network in its shell; take out the network of who agent, or the login")
	}
	if agentNet && len(m.Others()) > 0 {
		warn("rights: the agents of a module that reads the data of others never get the network in their shells; take out the network of who agent, or that data")
	}
	for _, t := range m.Tools.HTTP {
		right, subject := ToolRight(t)
		if subject != "" && !m.Declares(registry.Need{Right: right, Subject: subject}) {
			warn("http tool %q: it needs the right %s on %s; declare it in rights with why", t.Name, right, subject)
		}
		if t.Undo != "" && !m.knownAction(t.Undo) {
			warn("http tool %q undo: %q is neither a command nor a tool of this module", t.Name, t.Undo)
		}
	}
	for _, c := range m.Commands {
		if c.Executor.Type == "model" {
			if use := cmp.Or(c.Executor.Model, "@"+c.Executor.Kind); !m.Declares(registry.Need{Right: Money, Subject: use}) {
				warn("command %q: its model goes through %s; declare it under money in rights with why", c.Name, use)
			}
		}
		if f := c.Executor.Folder; !f.IsZero() && !m.Declares(registry.Need{Right: Files, Subject: cmp.Or(f.Name, "*")}) {
			warn("command %q: its agent works in the folder %s; declare it under files in rights with why", c.Name, cmp.Or(f.Name, "*"))
		}
		if c.Undo != "" && (c.Undo == c.Name || !m.knownAction(c.Undo)) {
			warn("command %q undo: %q is neither another command nor a tool of this module", c.Name, c.Undo)
		}
	}
	for name, s := range m.Tools.MCP.Servers {
		for tool, r := range s.Right {
			if r != Network && !m.Declares(registry.Need{Right: r, Subject: name + "." + tool}) {
				warn("mcp server %q: tool %s needs the right %s; declare %s.%s under it in rights with why", name, tool, r, name, tool)
			}
		}
	}
}

// badSubject is why deck cannot read a subject of a right, empty when it can.
func (m *Manifest) badSubject(right, s string) string {
	switch right {
	case Network:
		host, literal := address(s)
		switch {
		case s == "*" || literal:
		case strings.ContainsAny(s, "/?#@ ") || host == "":
			return "a host is example.com or host:port, not a URL"
		default:
			if suffix, icann := publicsuffix.PublicSuffix(host); suffix == host && (icann || strings.Contains(host, ".")) {
				return "a public suffix would open every site under it; name the site"
			}
		}
	case Login:
		if !slices.Contains(SubscriptionCLIs, s) {
			return fmt.Sprintf("a login is one of %s", strings.Join(SubscriptionCLIs, ", "))
		}
	case Files:
		if s != GPU && s != "*" && !schemas.Name.MatchString(s) {
			return "files are gpu or the name of a folder of the project"
		}
	}
	return ""
}

// SubscriptionCLIs are the CLIs that run on the owner's subscription login, the subjects of login.
var SubscriptionCLIs = []string{"claude-code", "codex"}

// address is the host of a domain without its port, and whether the domain is an IP address or a network.
func address(d string) (host string, literal bool) {
	host = d
	if h, _, err := net.SplitHostPort(d); err == nil {
		host = strings.Trim(h, "[]")
	}
	_, prefixErr := netip.ParsePrefix(d)
	_, ipErr := netip.ParseAddr(strings.Trim(host, "[]"))
	return host, prefixErr == nil || ipErr == nil
}
