package project

import (
	"context"
	"deck2/internal/app/access"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// widens lists what a change by the actor would grant an enabled project; the owner and drafts are not asked.
func (pj *Service) widens(ctx context.Context, q *db.Queries, act registry.Actor, projectID int64, next *manifest.Manifest) ([]string, error) {
	if act.Kind == registry.Owner {
		return nil, nil
	}
	p, err := q.GetProject(ctx, projectID)
	if err != nil || p.Status != store.ProjectEnabled {
		return nil, err
	}
	prev, err := docs.Manifest(ctx, q, p)
	if err != nil {
		prev = &manifest.Manifest{}
	}
	return manifest.Widens(prev, next), nil
}

// ask freezes a change that widens rights until the owner allows it; the frozen call applies only while the
// manifest is still at revision.
func (pj *Service) ask(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project, revision int64, widens []string, reason string, call Call) (Saved, error) {
	input := map[string]any{"if_revision": revision}
	for k, v := range call.Input {
		input[k] = v
	}
	raw, _ := json.Marshal(input)
	f := gateway.Freeze(act, call.Command, raw)
	f.As, f.Reason, f.Widens = gateway.AsOwner, reason, widens
	text := fmt.Sprintf("%s wants to change the manifest of %s (%s); it widens: %s", act.Author(), p.Name,
		orText(reason, "no reason given"), strings.Join(widens, "; "))
	row, err := gateway.Hold(ctx, q, act, p.ID, ask.KindGrant, registry.Internal, text, f, time.Time{})
	return Saved{Waiting: row.ID, Widens: widens}, err
}

// AnswerGrant makes the frozen call of an allowed grant as the owner; the grant closes only when it applied.
func (pj *Service) AnswerGrant(ctx context.Context, act registry.Actor, q db.Ask, answer string) (any, error) {
	switch {
	case answer == "allow":
		out, err := pj.Gateway.Decide(ctx, act, q, answer, nil)
		switch {
		case errors.Is(err, ask.ErrNotOpen):
			return nil, ask.NotOpen
		case registry.Status(err) == 409:
			return nil, registry.Conflict("deck: the manifest changed after this request; deny it and ask again")
		}
		return out, err
	case answer == "deny" || strings.HasPrefix(answer, "deny:"):
		if err := pj.Broker.Answer(ctx, q.ID, answer); err != nil {
			if errors.Is(err, ask.ErrNotOpen) {
				return nil, ask.NotOpen
			}
			return nil, err
		}
		pj.Gateway.Record(ctx, q, "rejected", "the owner said no")
		return map[string]any{"ok": true}, nil
	}
	return nil, registry.Invalid("deck: answer a grant with allow, deny or deny:<reason>")
}

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
			return registry.Invalid("deck: the manifest of %s does not load: %v", p.Name, err)
		}
		if m.Revision != in.Revision {
			return registry.Conflict("deck: the manifest of %s is at revision %d now, not %d; look at its rights again", p.Name, m.Revision, in.Revision)
		}
		n, err := q.SetProjectStatus(ctx, db.SetProjectStatusParams{Status: store.ProjectEnabled, ID: p.ID})
		if err != nil || n == 0 {
			if err == nil {
				err = registry.Conflict("deck: project %s is already enabled", p.Name)
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
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": p.Name, "status": store.ProjectEnabled, "rights": rights(m)}, nil
}

type projectAutonomyIn struct {
	Project  string `type:"project" desc:"Project name"`
	Autonomy string `enum:"suggest|auto" desc:"suggest or auto"`
}

// projectAutonomy writes the autonomy of a project into a new revision of its manifest, the rest as it was.
func (pj *Service) projectAutonomy(ctx context.Context, act registry.Actor, in projectAutonomyIn) (any, error) {
	p, err := access.Project(ctx, pj.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	d, err := pj.St.Q.FindDocument(ctx, docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName}.Find())
	if err != nil {
		return nil, err
	}
	r, err := docs.Revision(ctx, pj.St.Q, d, 0)
	if err != nil {
		return nil, err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.Text), &doc); err != nil {
		return nil, registry.Invalid("deck: the manifest of %s is not a JSON object: %v", p.Name, err)
	}
	delete(doc, "autonomy")
	if in.Autonomy == "auto" {
		doc["autonomy"] = json.RawMessage(`"auto"`)
	}
	raw, _ := json.MarshalIndent(doc, "", "  ")
	reason := "autonomy: " + in.Autonomy
	return pj.SaveManifest(ctx, act, p, raw, reason, 0, d.Current,
		Call{Command: registry.Builtin + ".manifest_save", Input: map[string]any{"project": p.Name, "manifest": string(raw), "reason": reason}})
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
	Live    bool   `opt:"" desc:"The project is enabled: the trial writes into its real records and cards"`
}

// commandTry starts a trial run of a project command: every call that is not internal is declined, so a token
// that may try runs any project command this way, though not a command closed to its kind of caller.
func (pj *Service) commandTry(ctx context.Context, act registry.Actor, in commandTryIn) (any, error) {
	probe := act
	probe.Token, probe.Commands = 0, nil
	c, input, err := pj.Registry.Resolve(ctx, probe, in.Command, json.RawMessage(in.Input))
	switch {
	case err != nil:
		return nil, err
	case c.ProjectID == 0:
		return nil, registry.Invalid("deck: only project commands have trial runs")
	case c.Fingerprint != nil:
		return nil, registry.Invalid("deck: %s is a tool, the call itself; only commands have trial runs", c.Name)
	}
	if err := pj.liveTrial(ctx, c, in.Live); err != nil {
		return nil, err
	}
	act.Trial = true
	return c.Handler(ctx, act, input)
}

// liveTrial lets a trial into an enabled project only when asked for, and never beside a real run of the command.
func (pj *Service) liveTrial(ctx context.Context, c *registry.Command, live bool) error {
	p, err := pj.St.Q.GetProject(ctx, c.ProjectID)
	if err != nil || p.Status == store.ProjectDraft {
		return err
	}
	if !live {
		return registry.Invalid("deck: %s is enabled: a trial writes into its real records and cards and may run beside its own runs; "+
			"try the change in a draft copy (project_import of its folder under another name), or pass live to try it here", p.Name)
	}
	open, err := pj.St.Q.ListOpenProcesses(ctx, db.ListOpenProcessesParams{WorkspaceID: p.WorkspaceID, ProjectID: p.ID})
	if err != nil {
		return err
	}
	for _, o := range open {
		if o.Command == strings.TrimPrefix(c.Name, p.Name+".") && o.Trial == 0 {
			return registry.Conflict("deck: %s is running now (process %d); try it once that run is over", c.Name, o.ID)
		}
	}
	return nil
}

func orText(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func (pj *Service) draftsCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("project_enable", "Enable project",
			"Turns a draft on: its triggers start running and the rights its manifest asks for count as allowed.",
			registry.OwnerOnly, pj.projectEnable),
		registry.Def("project_autonomy", "Project autonomy",
			"Sets whether a project suggests, so the owner says yes to its reversible changes, or acts on its own and reports; a new manifest revision.",
			registry.OwnerOnly, pj.projectAutonomy),
		registry.Def("command_try", "Try a command",
			"Runs a command of a project once as a trial to check it before a button or a schedule runs it: calls that are not internal are declined, nothing leaves deck.",
			registry.OwnerSide, pj.commandTry),
	}
}
