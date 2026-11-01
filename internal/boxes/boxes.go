// Package boxes is the one place the rights of a module become the box of its program.
package boxes

import (
	"slices"

	"deck2/internal/manifest"
	"deck2/internal/sandbox"
)

// For is the box of a program of kind under the rights in force granted and the folders of its project: what its
// door reaches, what of the owner's it reads, the graphics card; the caller adds the program and its own folders.
func For(kind string, granted *manifest.Manifest, folders []string) sandbox.Box {
	b := sandbox.Box{Kind: kind, Read: slices.Clone(folders)}
	if granted != nil {
		b.GPU = granted.GPU
	}
	switch kind {
	case sandbox.KindScript:
		b.Hosts = append([]string{}, granted.Domains.Read...)
	case sandbox.KindMCP:
		b.Hosts = granted.Domains.All()
	case sandbox.KindAgent:
		b.Hosts = manifest.ShellHosts(granted)
	case sandbox.KindBrowser:
		b.Hosts, b.Read = []string{"*"}, nil
	}
	return b
}
