package manifest

import (
	"deck2/schemas"
	"encoding/json"
	"regexp"
	"slices"
)

// SecretPrefix begins a reference to a secret, $secret.<name>.
const SecretPrefix = "$secret."

// SecretRef finds a reference to a secret; its group is the name.
var SecretRef = regexp.MustCompile(regexp.QuoteMeta(SecretPrefix) + `(` + schemas.SecretChar + `+)`)

// Secrets are the secrets the tools and MCP servers of a manifest use, sorted.
func (m *Manifest) Secrets() []string {
	all := serverSecrets(m)
	for _, t := range m.Tools.HTTP {
		all = append(all, toolSecrets(t)...)
	}
	slices.Sort(all)
	return slices.Compact(all)
}

// Keys are the secrets of Secrets the owner sets: all but the tokens an oauth2 sign-in keeps.
func (m *Manifest) Keys() []string {
	var signed []string
	for _, t := range m.Tools.HTTP {
		if a := t.AuthOf(); a.Type == "oauth2" {
			signed = append(signed, a.Token)
		}
	}
	return slices.DeleteFunc(m.Secrets(), func(n string) bool { return slices.Contains(signed, n) })
}

// serverSecrets are the secrets MCP servers of a manifest get in their environment.
func serverSecrets(m *Manifest) []string {
	b, _ := json.Marshal(m.Tools.MCP)
	var out []string
	for _, s := range SecretRef.FindAllSubmatch(b, -1) {
		out = append(out, string(s[1]))
	}
	return out
}

// toolSecrets are the secrets an HTTP tool sends: $secret.<name> in its request and the names in auth.
func toolSecrets(t HTTPTool) []string {
	b, _ := json.Marshal([]any{t.URL, t.Headers, t.Body})
	var out []string
	for _, s := range SecretRef.FindAllSubmatch(b, -1) {
		out = append(out, string(s[1]))
	}
	a := t.AuthOf()
	for _, n := range []string{a.Secret, a.ClientID, a.ClientSecret, a.Token} {
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}
