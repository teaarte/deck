// Package boxes is the one place the rights of a module become the box of its program.
package boxes

import (
	"slices"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
)

// For is the box of a program of kind of a module that declares m and holds g: door, folders, graphics card. The shell
// of an agent on a subscription login, or of a module that reads the data of others, reaches nothing by itself.
func For(kind string, m *manifest.Manifest, g manifest.Grants, login bool, folders []string) sandbox.Box {
	b := sandbox.Box{Kind: kind, Read: slices.Clone(folders)}
	if kind == sandbox.KindBrowser {
		b.Hosts, b.Read = []string{"*"}, nil
		return b
	}
	if m == nil {
		return b
	}
	b.GPU = m.Holds(g, registry.Need{Right: manifest.Files, Subject: manifest.GPU})
	switch kind {
	case sandbox.KindScript, sandbox.KindMCP:
		b.Hosts = m.Held(g, manifest.Network, manifest.Programs)
	case sandbox.KindAgent:
		if !login && len(m.Others()) == 0 {
			b.Hosts = m.Held(g, manifest.Network, manifest.Agents)
		}
	}
	return b
}
