package process

import (
	"context"
	"encoding/json"

	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/fault"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// runTool makes the one gateway tool call of a tool command with the command input as it is.
func (r *Runner) runTool(ctx context.Context, t turn, p db.Process) {
	name := t.cmd.Executor.Tool
	r.runCall(ctx, t, p, true, func(proj db.Project) (string, json.RawMessage, error) {
		if !registry.IsBuiltin(name) {
			name = proj.Name + "." + name
		}
		r.deps.Places.Servers(context.WithoutCancel(ctx), t.project)
		tool, err := r.gw.Command(context.WithoutCancel(ctx), proj.WorkspaceID, name)
		if err == nil {
			err = manifest.FitsTool(*t.cmd, name, tool.Input)
		}
		return name, json.RawMessage(t.input), err
	})
}

// runModel makes the one call of a model of a model command: its prompt, the command input, and the result
// schema for text; the words of tts, the recording of stt and the frames of a video are input fields.
func (r *Runner) runModel(ctx context.Context, t turn, p db.Process) {
	r.runCall(ctx, t, p, false, func(db.Project) (string, json.RawMessage, error) {
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
		case "stt", "image":
			for _, f := range manifest.FieldsOf(t.cmd.Input, "file") {
				call["file"] = input[f]
				delete(input, f)
			}
		case "video":
			for _, f := range manifest.FieldsOf(t.cmd.Input, "file") {
				if v, ok := input[f]; ok {
					call[f] = v
				}
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
		return registry.Deck("model_call"), b, nil
	})
}

// runCall makes the one gateway call of a command whose executor is a call: what call names with its input; a tool
// no stricter than its command is cleared with it, a model keeps to its own limits whatever the command's class.
func (r *Runner) runCall(ctx context.Context, t turn, p db.Process, tool bool, call func(db.Project) (string, json.RawMessage, error)) {
	bg := context.WithoutCancel(ctx)
	id := t.process
	u := &run{turn: t}
	proj, err := r.st.Q.GetProject(bg, t.project)
	if err != nil {
		r.apply(bg, u.process, Settle{Run: u, F: fault.Of(err)})
		return
	}
	name, input, err := call(proj)
	if err != nil {
		r.apply(bg, u.process, Settle{Run: u, F: fault.Of(err)})
		return
	}
	u, f := r.begin(bg, t, proj, nil)
	if f != nil {
		r.apply(bg, t.process, Settle{Run: &run{turn: t}, F: f})
		return
	}
	defer u.close(r)
	act := registry.ProcessOf(p, proj)
	var out registry.Result
	if tool {
		out, err = r.gw.CallTool(ctx, act, proj.Name+"."+t.cmd.Name, name, input)
	} else {
		out, err = r.gw.Call(ctx, gateway.Call{Who: act, Command: name, Input: input})
	}
	if r.cutRun(bg, ctx, u) {
		return
	}
	if err != nil {
		r.apply(bg, u.process, Settle{Run: u, F: fault.OfCall(err)})
		return
	}
	if out.Effect == registry.Waiting {
		r.park(bg, id)
		return
	}
	res, _ := json.Marshal(out)
	r.apply(bg, u.process, Settle{Run: u, Result: string(callResult(res))})
}

// callResult is what a call gave as the result of its process: a file the tool saved is a file of the result.
func callResult(res json.RawMessage) json.RawMessage {
	var v map[string]any
	if json.Unmarshal(res, &v) == nil {
		if f, ok := v["file"].(string); ok && v["files"] == nil {
			v["files"] = []FileRef{{Path: f}}
			res, _ = json.Marshal(v)
		}
	}
	return res
}

// deliverTool closes a tool process that waited for the owner with what the frozen call gave.
func (r *Runner) deliverTool(ctx context.Context, p db.Process, pending untold) error {
	if ok, err := r.claim(ctx, p.ID, WaitAnswer); err != nil || !ok {
		return err
	}
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return pending.ids().mark(ctx, q, r.now()) })
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
			r.apply(ctx, p.ID, Fail{F: fault.New(fault.RightsDenied, "deck: the owner did not confirm the call in time; it was not made")})
		case row.Result == "":
			r.apply(ctx, p.ID, Fail{F: fault.New(fault.RightsDenied, gateway.RejectMessage(a.Answer))})
		case o.Error != "" || cmd == nil:
			r.apply(ctx, p.ID, Fail{F: fault.New(fault.TaskFailed, o.Error)})
		default:
			r.apply(ctx, p.ID, Settle{Run: &run{turn: turn{process: p.ID, project: p.ProjectID, cmd: cmd}}, Result: string(callResult(o.Result))})
		}
		return nil
	}
	r.apply(ctx, p.ID, Fail{F: fault.New(fault.DeckInternal, "deck: the tool call got no decision")})
	return nil
}
