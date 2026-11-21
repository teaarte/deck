package process

import (
	"context"

	"deck2/internal/manifest"
	"deck2/internal/store/db"
)

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
