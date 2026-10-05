package sandbox

import "context"

// OpenHint is where the owner opens a folder of their home to the programs of a project.
const OpenHint = "Of the owner's home a script or MCP server reads only the runtimes on PATH and the folders of its project; the owner adds a folder in Settings → Projects."

// Rules are what deck's programs are confined by beside their policy: Kept hears of a path the sandbox kept closed
// from a program of a project.
type Rules struct {
	Kept func(ctx context.Context, project int64, who, path string)
}

// Report passes on a path the sandbox kept closed from who of a project.
func (r Rules) Report(ctx context.Context, project int64, who, path string) {
	if r.Kept != nil {
		r.Kept(ctx, project, who, path)
	}
}
