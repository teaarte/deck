package screens

import (
	"context"

	"deck2/internal/manifest"
	"deck2/internal/store/db"
)

// Deck is what the screen sources read from the rest of deck.
type Deck interface {
	Project(ctx context.Context, p db.Project) (Project, *manifest.Manifest)
	Providers(ctx context.Context, workspace int64) ([]Provider, error)
	Health(ctx context.Context, workspace int64) ([]map[string]any, error)
	Secrets(ctx context.Context, workspace int64) ([]string, error)
	Triggers(ctx context.Context, workspace int64) ([]Trigger, error)
	Settings(ctx context.Context, workspace int64) []map[string]any
	Calls(ctx context.Context, workspace, project int64, outcome string) ([]map[string]any, error)
	Snapshots() ([]map[string]any, error)
	Waits(ctx context.Context, project int64) (map[int64][]int64, error)
	Chat(ctx context.Context, workspace int64, topic string) (any, error)
}

// Project is a project as its row on the Projects tab shows it.
type Project struct {
	Description   string
	Revision      int64
	Rights        []string
	ManifestError string
	// Folders are "name: path" of each folder of the project.
	Folders []string
}

// Provider is a provider of the workspace with what the owner may do with it.
type Provider struct {
	Name, Agent, Model, URL         string
	Status, Verdict, Version, Error string
	Speed                           float64
	Caps                            []string
	Builtin, SelfCheck              bool
}
