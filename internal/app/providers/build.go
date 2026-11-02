package providers

import (
	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/provider/claudecode"
	"deck2/internal/provider/codex"
	"deck2/internal/sandbox"
)

// Build are the built-in providers of the deck whose folder is home, in the boxes of sb: Claude Code first, then Codex.
func Build(home string, sb *sandbox.Sandbox) []provider.Provider {
	return []provider.Provider{claudecode.New(home, sb), codex.New(home, sb)}
}

// agent is an ACP agent on the program deck was given for it or finds by its name, in deck's folder and sandbox.
func (s *Service) agent(c acp.Config) (*acp.Adapter, error) {
	c.Binary, c.Home, c.Sandbox = s.ACPBinaries[c.Agent], s.Home, s.Sandbox
	return acp.New(c)
}

// Installed says deck finds the program of an ACP agent.
func (s *Service) Installed(agent string) bool {
	bin := s.ACPBinaries[agent]
	if bin == "" {
		bin = agent
	}
	_, err := sandbox.Find(bin)
	return err == nil
}
