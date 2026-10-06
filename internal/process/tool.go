package process

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// runTool makes the one gateway tool call of a tool command with the command input as it is.
func (r *Runner) runTool(ctx context.Context, t turn, trial bool) {
	name := t.cmd.Executor.Tool
	r.runCall(ctx, t, trial, true, func(proj db.Project) (string, json.RawMessage, error) {
		if !strings.HasPrefix(name, registry.Builtin+".") {
			name = proj.Name + "." + name
		}
		r.deps.Servers(context.WithoutCancel(ctx), t.project)
		tool, err := r.gw.Command(context.WithoutCancel(ctx), proj.WorkspaceID, name)
		if err == nil {
			err = manifest.FitsTool(*t.cmd, name, tool.Input)
		}
		return name, json.RawMessage(t.input), err
	})
}

// runModel makes the one call of a model of a model command: its prompt, the command input, and the result
// schema for text; the words of tts and the recording of stt are input fields.
func (r *Runner) runModel(ctx context.Context, t turn, trial bool) {
	r.runCall(ctx, t, trial, false, func(db.Project) (string, json.RawMessage, error) {
		prompt, err := r.commandBody(context.WithoutCancel(ctx), t.project, t.cmd)
		if err != nil {
			return "", nil, err
		}
		e := t.cmd.Executor
		var input map[string]any
		_ = json.Unmarshal([]byte(t.input), &input)
		call := map[string]any{"kind": e.Kind, "prompt": prompt}
		if e.Model != "" {
			call["model"] = e.Model
		}
		if len(e.Options) > 0 {
			b, _ := json.Marshal(e.Options)
			call["options"] = string(b)
		}
		switch e.Kind {
		case "tts":
			call["text"] = input["text"]
			delete(input, "text")
		case "stt":
			for _, f := range manifest.FileFields(t.cmd.Input) {
				call["file"] = input[f]
				delete(input, f)
			}
		case "text":
			if len(t.cmd.Result) > 0 {
				call["schema"] = string(t.cmd.Result)
			}
		}
		if len(input) > 0 {
			b, _ := json.Marshal(input)
			call["input"] = string(b)
		}
		b, _ := json.Marshal(call)
		return registry.Builtin + ".model_call", b, nil
	})
}

// runCall makes the one gateway call of a command whose executor is a call: what call names with its input; a tool
// no stricter than its command is cleared with it, a model keeps to its own limits whatever the command's class.
func (r *Runner) runCall(ctx context.Context, t turn, trial, tool bool, call func(db.Project) (string, json.RawMessage, error)) {
	bg := context.WithoutCancel(ctx)
	id := t.process
	dir := r.runDir(id)
	fail := func(err error) {
		r.clearRun(dir)
		r.finish(bg, id, StateError, "", false, err.Error())
	}
	proj, err := r.st.Q.GetProject(bg, t.project)
	if err != nil {
		fail(err)
		return
	}
	if err := os.MkdirAll(filepath.Join(dir, "results"), 0o700); err != nil {
		fail(err)
		return
	}
	name, input, err := call(proj)
	if err != nil {
		fail(err)
		return
	}
	act := registry.Actor{Kind: registry.Agent, Workspace: proj.WorkspaceID, ProjectID: t.project, ProcessID: id, Trial: trial}
	var out any
	if tool {
		out, err = r.gw.CallTool(ctx, act, proj.Name+"."+t.cmd.Name, name, input)
	} else {
		out, err = r.gw.CallAs(ctx, act, name, input)
	}
	if r.cut(bg, ctx, id) {
		if r.ctx.Err() == nil {
			r.clearRun(dir)
		}
		return
	}
	if err != nil {
		fail(err)
		return
	}
	if _, ok := out.(gateway.Waiting); ok {
		r.park(bg, id)
		return
	}
	res, _ := json.Marshal(out)
	r.toolResult(bg, t.project, t.cmd, id, res)
}

// toolResult closes a tool process with what its call gave; a file the tool saved belongs to the process.
func (r *Runner) toolResult(ctx context.Context, projectID int64, cmd *manifest.Command, id int64, res json.RawMessage) {
	dir := r.runDir(id)
	defer r.clearRun(dir)
	var v map[string]any
	if json.Unmarshal(res, &v) == nil {
		if f, ok := v["file"].(string); ok && v["files"] == nil {
			v["files"] = []FileRef{{Path: f}}
			res, _ = json.Marshal(v)
		}
	}
	if verr := validate(cmd, res); verr != nil {
		r.finish(ctx, id, StateDone, string(res), true, "")
		r.warn(ctx, id, fmt.Sprintf("The result of tool command %s does not match its schema, so nothing acts on it: %v", cmd.Name, verr))
		return
	}
	if err := r.keepFiles(ctx, projectID, id, filepath.Join(dir, "results"), string(res)); err != nil {
		r.finish(ctx, id, StateError, "", false, err.Error())
		return
	}
	r.finish(ctx, id, StateDone, string(res), false, "")
}

// deliverTool closes a tool process that waited for the owner with what the frozen call gave.
func (r *Runner) deliverTool(ctx context.Context, p db.Process, pending untold) error {
	if ok, err := r.claim(ctx, p.ID, StateWaitingAnswer); err != nil || !ok {
		return err
	}
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return pending.ids().mark(ctx, q) })
	m, err := docs.ManifestAt(ctx, r.st.Q, p.ProjectID, p.ManifestRevision)
	var cmd *manifest.Command
	if err == nil {
		cmd = m.Command(p.Command)
	}
	for _, row := range pending.asks {
		a := row.Ask
		if a.Kind != ask.KindCall {
			continue
		}
		var o gateway.Outcome
		_ = json.Unmarshal([]byte(row.Result), &o)
		switch {
		case a.State == ask.StateExpired || a.State == ask.StateDismissed:
			r.finish(ctx, p.ID, StateError, "", false, "deck: the owner did not confirm the call in time; it was not made")
		case row.Result == "":
			r.finish(ctx, p.ID, StateError, "", false, gateway.RejectMessage(a.Answer))
		case o.Error != "" || cmd == nil:
			r.finish(ctx, p.ID, StateError, "", false, o.Error)
		default:
			r.toolResult(ctx, p.ProjectID, cmd, p.ID, o.Result)
		}
		return nil
	}
	r.finish(ctx, p.ID, StateError, "", false, "deck: the tool call got no decision")
	return nil
}
