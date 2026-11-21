// Package rights keeps the rights of modules: the rows of what each declares with the owner's answers, the answers
// and their report, and what enabling a module grants it.
package rights

import (
	"slices"

	"deck2/internal/registry"
	"deck2/internal/store"
)

// Service keeps the rights of the modules of a workspace and the owner's answers to them.
type Service struct {
	St       *store.Store
	Registry *registry.Registry
	// StopServer stops a running MCP server by its key.
	StopServer func(key string)
}

// Commands are the commands of rights.
func (s *Service) Commands() []*registry.Command {
	return slices.Concat(s.rightsCommands(), s.reportCommands())
}
