package manifest

import (
	"slices"
)

// AgentNetwork is what the agents of m reach on Claude Code or Codex, whose shells take no list of hosts: the whole
// network unless m has no_network; empty without agent commands.
func AgentNetwork(m *Manifest) string {
	if m.NoNetwork || !slices.ContainsFunc(m.Commands, func(c Command) bool { return c.Executor.Type == "agent" }) {
		return ""
	}
	return "agents on Claude Code or Codex: the whole network, whatever domains.read names"
}

// Registries are the package registries a manifest reaches by naming them, before any grant.
var Registries = []string{"pypi.org", "files.pythonhosted.org", "registry.npmjs.org", "proxy.golang.org", "sum.golang.org"}

// AgentHosts are what the shell of an agent reaches besides the read domains of its project: the registries,
// GitHub, where code and its packages come from, and Hugging Face, where models do.
var AgentHosts = append(slices.Clone(Registries), "github.com", "codeload.github.com", "objects.githubusercontent.com",
	"huggingface.co", "hf.co")

// ShellHosts are the hosts the shell of an agent of m reaches: none without network, every one for "*" in read.
func ShellHosts(m *Manifest) []string {
	switch {
	case m.NoNetwork:
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
