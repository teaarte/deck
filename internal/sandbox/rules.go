package sandbox

import "context"

// OpenHint is where the owner opens a folder of their home to the programs of a project.
const OpenHint = "Of the owner's home a script or MCP server reads only the runtimes on PATH and the folders of its project; the owner adds a folder in Settings → Projects."

// Rules are what deck's programs are confined by beside their policy: Open runs those of a workspace in the old
// sandbox, which reads the home folder; Kept hears of a path the sandbox kept closed from a program of a project.
type Rules struct {
	Open func(ctx context.Context, workspace int64) bool
	Kept func(ctx context.Context, project int64, who, path string)
}

// IsOpen is whether the programs of the workspace run in the old sandbox.
func (r Rules) IsOpen(ctx context.Context, workspace int64) bool {
	return r.Open != nil && r.Open(ctx, workspace)
}

// Report passes on a path the sandbox kept closed from who of a project.
func (r Rules) Report(ctx context.Context, project int64, who, path string) {
	if r.Kept != nil {
		r.Kept(ctx, project, who, path)
	}
}
