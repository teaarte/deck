// Package work keeps who does what of the work of deck: the models assigned to each kind of work, where a call of a
// model goes and what it cost, the calls of models, the web of agents and speech to text.
package work

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"deck2/internal/files"
	"deck2/internal/home"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Service keeps the work; the functions of providers say who exists and whether it is up.
type Service struct {
	St   *store.Store
	Now  func() time.Time
	Home home.Layout
	// Hold keeps a snapshot from copying the base between the row of a file of a model and its blob, until its unlock.
	Hold func() func()
	// VideoPoll is how often a job of a video model is asked after, VideoCeiling how long deck waits for it.
	VideoPoll, VideoCeiling time.Duration
	// Key is the API key of a connection, "" for none.
	Key func(workspace int64, connection string) string
	// Resolve finds a file an input points to for a process of a project.
	Resolve func(ctx context.Context, project, process int64, ref string) (files.Ref, error)
	// Invoke calls a command, as speech to text calls a model.
	Invoke func(ctx context.Context, act registry.Actor, name string, input json.RawMessage) (any, error)
	// Builtin are the providers deck was started with, the main one first.
	Builtin []provider.Provider
	// Provider is a provider of a workspace by name, All every one that is on, Off why one takes no work.
	Provider func(ctx context.Context, workspace int64, name string) (provider.Provider, error)
	All      func(ctx context.Context, workspace int64) ([]provider.Provider, error)
	Off      func(ctx context.Context, workspace int64, name string) string
	// Up says a new start may pick a provider, Health is its health row, Exists says deck has it.
	Up     func(ctx context.Context, workspace int64, name string) bool
	Health func(ctx context.Context, workspace int64, name string) (db.Health, error)
	Exists func(ctx context.Context, workspace int64, name string) bool
	// Installed says deck finds the program of an ACP agent.
	Installed func(agent string) bool
	// Node carries the calls of a connection over the connection of the node that serves it; Online says the node
	// is connected.
	Node   func(device int64) http.RoundTripper
	Online func(device int64) bool
	// FolderNodes are the nodes that hold the folder of a project an agent command works in, and whether deck's
	// machine holds it too.
	FolderNodes func(ctx context.Context, projectID int64, f manifest.Folder) (nodes []string, deck bool, err error)
	// Go runs work beside the calls; false once deck stops.
	Go func(func(context.Context)) bool

	// plain are the models whose API refused the reasoning an agent hands back, by address and model, while deck runs.
	plain sync.Map
	jobs  sync.Map
}

// Commands are the commands of the work: assignments, calls of models, the web of agents and speech to text.
func (s *Service) Commands() []*registry.Command {
	return slices.Concat(s.listCommands(), s.assignmentCommands(), s.callCommands(), s.webCommands(), s.transcribeCommands())
}

// plainOf is the flag of a model whose API takes no reasoning back, kept while deck runs.
func (s *Service) plainOf(key string) *atomic.Bool {
	v, _ := s.plain.LoadOrStore(key, &atomic.Bool{})
	return v.(*atomic.Bool)
}
