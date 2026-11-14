package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"

	"deck2/internal/fault"
	"deck2/internal/provider"
	"deck2/internal/script"
)

// Scripts runs the scripts of a project on a node, where the folders of the project are: their run folder and the
// data of the project go to the node and what the script changed comes back, as for a turn.
type Scripts struct{ Machine }

// Run runs the script of j on the node in a box of its own, reading the folders of j.Box.Read on the node's machine;
// side is deck's side of the script, emit tells the feed that it waits for the node.
func (s *Scripts) Run(ctx context.Context, j script.Job, side http.Handler, emit func(provider.Event)) ([]byte, error) {
	say := func(line string) {
		if j.OnLine != nil {
			j.OnLine(line)
		}
	}
	dir, err := s.rel(j.Dir)
	roots := []string{dir}
	for _, p := range append([]string{s.Home.Data(j.Project)}, j.Read...) {
		r, rerr := s.rel(p)
		err = errors.Join(err, rerr)
		if !under(r, roots) {
			roots = append(roots, r)
		}
	}
	if err != nil {
		return nil, err
	}
	session := "script-" + filepath.Base(j.Dir)
	if !s.online() {
		if back, err := s.away(ctx, emit, "is not connected", j); !back {
			return nil, err
		}
	}
	var sent map[string]stamp
	for tried := false; ; tried = true {
		var err error
		sent, err = s.send(ctx, session, roots)
		var r *refused
		var big *tooBig
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case errors.As(err, &big):
			return nil, fault.New(fault.ScriptFailed, "script "+j.Script.Name+": "+err.Error())
		case errors.As(err, &r) || tried:
			return nil, fault.New(fault.Network, fmt.Sprintf("node %s did not take the files of script %s: %v", s.Node, j.Script.Name, err))
		default:
			if back, err := s.away(ctx, emit, "went away ("+err.Error()+")", j); !back {
				return nil, err
			}
			continue
		}
		break
	}
	tok, end := s.Hub.Side(s.Device, side)
	defer end()
	// The script calls deck with the token of its side; deck's side puts in that of the script.
	call := scriptCall{Project: j.Project, Script: *j.Script, Source: j.Source, Draft: j.Draft, Dir: dir, Stdin: j.Stdin, Hosts: j.Box.Hosts,
		GPU: j.Box.GPU, Folders: j.Box.Read, Side: tok, Session: session}
	for _, p := range j.Read {
		r, _ := s.rel(p)
		call.Read = append(call.Read, r)
	}
	b, _ := json.Marshal(call)
	resp, err := s.call(ctx, http.MethodPost, scriptPath, nil, bytes.NewReader(b))
	if err != nil {
		var r *refused
		if errors.As(err, &r) && r.folder {
			s.denied(r)
			return nil, fault.New(fault.RightsDenied, fmt.Sprintf("node %s refused script %s: %s", s.Node, j.Script.Name, r.text))
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fault.New(fault.Network, fmt.Sprintf("node %s did not run script %s: %v", s.Node, j.Script.Name, err))
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var f scriptFrame
		if err := dec.Decode(&f); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fault.New(fault.Network, fmt.Sprintf("node %s went away during script %s: %v", s.Node, j.Script.Name, err))
		}
		switch {
		case f.Line != nil:
			say(*f.Line)
		case f.Done != nil:
			s.bring(context.WithoutCancel(ctx), session, roots, sent, say)
			switch {
			case f.Done.Fault != nil:
				return nil, f.Done.Fault
			case f.Done.Error != "":
				return nil, errors.New(f.Done.Error)
			}
			return f.Done.Out, nil
		}
	}
}

// away waits for the node of a script while it is away; back is false with the failure of the script when it did
// not come back in time.
func (s *Scripts) away(ctx context.Context, emit func(provider.Event), what string, j script.Job) (bool, error) {
	back, err := s.wait(ctx, emit, what, fmt.Sprintf("script %s waits up to %s for it", j.Script.Name, s.wait0()))
	if back || err != nil {
		return back, err
	}
	return false, fault.New(fault.Network, fmt.Sprintf("node %s %s and did not come back in %s; script %s ran nowhere", s.Node, what, s.wait0(), j.Script.Name))
}
