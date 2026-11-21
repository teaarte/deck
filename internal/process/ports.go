package process

import (
	"context"
	"slices"
	"time"

	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/store/db"
)

// Router says who answers a turn: the provider it starts on or continues with, the chain of its assignment, and the
// state of a provider; it hears what the turns of a provider told of it.
type Router interface {
	// For picks the provider a command starts on.
	For(ctx context.Context, projectID int64, cmd *manifest.Command) (provider.Provider, error)
	// Named is the provider a process started on; its session belongs to it.
	Named(ctx context.Context, projectID int64, name string) (provider.Provider, error)
	// Chain is the assignment of a command: the providers that fit it in order, each with whether a new start may
	// pick it; empty when the owner assigned the command nothing.
	Chain(ctx context.Context, projectID int64, cmd *manifest.Command) []Candidate
	// State is what the health of a provider says for the turns of a workspace; the runner never picks.
	State(ctx context.Context, workspace int64, name string) Standing
	// Limit pauses the scope of a provider's usage limit until ends (unix seconds, zero when the answer said no
	// reset), says when the provider is called again and whether the turn waits that out on the same login.
	Limit(ctx context.Context, workspace int64, name string, ends int64, words string) (until time.Time, stay bool)
	// Heard hears what a turn told of its provider.
	Heard(ctx context.Context, workspace int64, name string, h Heard)
}

// Candidate is a provider of the assignment of a command; Up says a new start may pick it.
type Candidate struct {
	Name string
	Up   bool
}

// Standing is what the health of a provider says: Goes, that a turn already started may run on it now; Down and Why,
// the status and text of its own health when it is paused or red, so no turn goes on on it.
type Standing struct {
	Goes      bool
	Down, Why string
}

// Heard is what a turn told of its provider: whether it answered and the windows of the subscription a turn of a CLI
// told, or that it failed or lost its login, which is of the provider in every workspace.
type Heard struct {
	OK      bool
	Windows []provider.Window
	Broke   bool
}

// Brief is what deck gives a turn: its instructions, documents, skills, memory and language.
type Brief interface {
	Prompt(ctx context.Context, projectID int64) (string, error)
	// Text is the current text of a project document of the kind, of any kind for ""; empty when missing.
	Text(ctx context.Context, projectID int64, kind, name string) (string, error)
	// Skills are the files of the named skills: the project's own, else the workspace's, whose mark from holds.
	Skills(ctx context.Context, projectID int64, names []string) (files map[string]Text, err error)
	// Memory is the memory a fresh session of the process gets, as data: the indexes and the lessons of its command
	// in the memory of its project; empty when there are none. again is the same turn asking anew, its reads counted.
	Memory(ctx context.Context, p db.Process, again bool) (memory, lessons Text, err error)
	// Gives is the language the owner reads what a process says in, as a code; "" when deck does not know it.
	Gives(ctx context.Context, workspace, process int64) string
}

// Places are where the work of a project runs: its folders, the place of its scripts, its MCP servers and programs.
type Places interface {
	// Folder is the real path of the project folder an agent command works in, on the machine of its provider.
	Folder(ctx context.Context, projectID int64, f manifest.Folder, provider string) (string, error)
	// Paths are the paths of the folders of a project on the machine of a node, deck's own for 0.
	Paths(ctx context.Context, projectID, node int64) []string
	// ScriptsAt is where the scripts of a project run: on the node that holds its folders, else on deck.
	ScriptsAt(ctx context.Context, projectID int64) (Place, error)
	// Servers starts the MCP servers of a project whose tools deck has not listed yet.
	Servers(ctx context.Context, projectID int64)
	// Programs looks for the programs of the scripts of a project again and writes the health of its commands.
	Programs(ctx context.Context, projectID int64) error
}

// next is the provider of a chain after the one that refused its turn, the first that is up; empty for none.
func next(chain []Candidate, after string) string {
	if i := slices.IndexFunc(chain, func(c Candidate) bool { return c.Name == after }); i >= 0 {
		chain = chain[i+1:]
	}
	for _, c := range chain {
		if c.Name != after && c.Up {
			return c.Name
		}
	}
	return ""
}

// ahead is the provider of a chain that is up and stands before the one a process runs on, any when that one is in
// the chain no longer; empty for none.
func ahead(chain []Candidate, current string) string {
	if i := slices.IndexFunc(chain, func(c Candidate) bool { return c.Name == current }); i >= 0 {
		chain = chain[:i]
	}
	for _, c := range chain {
		if c.Up {
			return c.Name
		}
	}
	return ""
}
