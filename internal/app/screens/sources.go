package screens

import (
	"deck2/internal/registry"
	"deck2/internal/store"
)

// Sources read what the blocks of screens show besides the commands they read: the commands of forms.
type Sources struct {
	Store    *store.Store
	Registry *registry.Registry
}
