package project

import (
	"context"
	"deck2/internal/app/access"
	"deck2/internal/health"
	"encoding/json"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

type projectEnableIn struct {
	Project  string `type:"project" desc:"Project name"`
	Revision int64  `desc:"The manifest revision whose rights the owner looked at"`
}

// projectEnable turns a draft on at the manifest revision the owner saw: its triggers start from now, and
// the rights of that revision count as allowed.
func (pj *Service) projectEnable(ctx context.Context, act registry.Actor, in projectEnableIn) (any, error) {
	p, err := access.Project(ctx, pj.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	var m *manifest.Manifest
	err = pj.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if m, err = docs.Manifest(ctx, q, p); err != nil {
			return registry.Invalid("the manifest of %s does not load: %v", p.Name, err)
		}
		if m.Revision != in.Revision {
			return registry.Conflict("the manifest of %s is at revision %d now, not %d; look at its rights again", p.Name, m.Revision, in.Revision)
		}
		n, err := q.SetProjectStatus(ctx, db.SetProjectStatusParams{Status: store.ProjectEnabled, ID: p.ID})
		if err != nil || n == 0 {
			if err == nil {
				err = registry.Conflict("project %s is already enabled", p.Name)
			}
			return err
		}
		if err := q.GrantProject(ctx, db.GrantProjectParams{GrantedRevision: store.ID(in.Revision), ID: p.ID}); err != nil {
			return err
		}
		rows, err := q.ListProjectTriggers(ctx, p.ID)
		if err != nil {
			return err
		}
		for _, t := range rows {
			if err := pj.Scheduler.Rewind(ctx, q, t); err != nil {
				return err
			}
			if err := health.Clear(ctx, q, health.Project(p), health.Trigger(p.Name, t.Command)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": p.Name, "status": store.ProjectEnabled, "rights": rights(m)}, nil
}

// rights are what a project is allowed beyond internal actions.
func rights(m *manifest.Manifest) []string {
	if w := manifest.Widens(&manifest.Manifest{}, m); w != nil {
		return w
	}
	return []string{}
}

type commandTryIn struct {
	Command string `desc:"Command name, project.command"`
	Input   string `type:"json" opt:"" desc:"Command input: a JSON object"`
	Live    bool   `opt:"" desc:"The project is enabled: the trial writes into its real records"`
	Only    bool   `opt:"" desc:"Only this command: the commands that run after it do not start; the whole chain runs when omitted"`
}

// commandTry starts a trial run of a project command: a call that is not internal is declined or, for a tool,
// waits for the owner; a token that may try runs any project command this way, though not one closed to it.
func (pj *Service) commandTry(ctx context.Context, act registry.Actor, in commandTryIn) (any, error) {
	probe := act
	probe.Kind, probe.Token, probe.Commands = act.As(), 0, nil
	c, input, err := pj.Registry.Resolve(ctx, probe, in.Command, json.RawMessage(in.Input))
	switch {
	case err != nil:
		return nil, err
	case c.ProjectID == 0:
		return nil, registry.Invalid("only project commands have trial runs")
	case c.Gate.Fingerprint != nil:
		return nil, registry.Invalid("%s is a tool, the call itself; only commands have trial runs", c.Name)
	}
	if err := pj.liveTrial(ctx, c, in.Live); err != nil {
		return nil, err
	}
	act.Trial, act.Alone = true, in.Only
	return c.Handler(ctx, act, input)
}

// liveTrial lets a trial into an enabled project only when asked for, and never beside a real run of the command.
func (pj *Service) liveTrial(ctx context.Context, c *registry.Command, live bool) error {
	p, err := pj.St.Q.GetProject(ctx, c.ProjectID)
	if err != nil || p.Status == store.ProjectDraft {
		return err
	}
	if !live {
		return registry.Invalid("%s is enabled: a trial writes into its real records and may run beside its own runs; "+
			"try the change in a draft copy (project_import of its folder under another name), or pass live to try it here", p.Name)
	}
	open, err := pj.St.Q.ListOpenProcesses(ctx, db.ListOpenProcessesParams{WorkspaceID: p.WorkspaceID, ProjectID: p.ID})
	if err != nil {
		return err
	}
	for _, o := range open {
		if o.Command == strings.TrimPrefix(c.Name, p.Name+".") && o.Trial == 0 {
			return registry.Conflict("%s is running now (process %d); try it once that run is over", c.Name, o.ID)
		}
	}
	return nil
}

func (pj *Service) draftsCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("project_enable", "Enable project",
			"Turns a draft on: its triggers start running and the rights its manifest asks for count as allowed.",
			registry.OwnerOnly, pj.projectEnable),
		registry.Def("command_try", "Try a command",
			"Runs a command of a project once as a trial to check it before a button or a schedule runs it: calls of commands that are not internal are declined; a call of a tool, or of a model that is neither free nor local, waits for the owner's yes on that call, internal or not.",
			registry.OwnerSide, pj.commandTry),
	}
}
