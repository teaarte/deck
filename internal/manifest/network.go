package manifest

import (
	"slices"
)

// Registries are the package registries a manifest reaches by naming them, before any grant.
var Registries = []string{"pypi.org", "files.pythonhosted.org", "registry.npmjs.org", "proxy.golang.org", "sum.golang.org"}

// AgentHosts are what the shell of an agent of a project with domains reaches besides them: the registries, GitHub,
// where code and its packages come from, and Hugging Face, where models do.
var AgentHosts = append(slices.Clone(Registries), "github.com", "codeload.github.com", "objects.githubusercontent.com",
	"huggingface.co", "hf.co")

// ShellHosts are the hosts the shell of an agent of m reaches besides its provider and deck: none without domains,
// every one for "*" in read.
func ShellHosts(m *Manifest) []string {
	switch {
	case len(m.Domains.Read) == 0:
		return nil
	case slices.Contains(m.Domains.Read, "*"):
		return []string{"*"}
	}
	out := slices.Clone(AgentHosts)
	for _, d := range m.Domains.Read {
		if !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}
