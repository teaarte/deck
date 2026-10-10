package chat

import (
	"context"
	"database/sql"
	"deck2/master"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// Memory is what a fresh session of a process gets of the memory: the index of its own project, as data; an
// agent of the master gets the owner's index and, in a topic about a project, the index of that project.
func (ch *Service) Memory(ctx context.Context, p db.Process) (string, error) {
	proj, err := ch.St.Q.GetProject(ctx, p.ProjectID)
	if err != nil {
		return "", err
	}
	if proj.Builtin == 1 {
		var in struct{ Topic int64 }
		_ = json.Unmarshal([]byte(p.Input), &in)
		text, _, err := ch.masterMemory(ctx, proj, in.Topic)
		return text, err
	}
	own, _, err := ch.memoryIndex(ctx, proj, "Memory index of project "+proj.Name)
	if err != nil || own == "" {
		return "", err
	}
	return master.Text("project_memory", struct{ Project, Index string }{proj.Name, own}), nil
}

// masterMemory is the memory an agent of the master gets, with a sign that changes when the memory does.
func (ch *Service) masterMemory(ctx context.Context, mp db.Project, topic int64) (string, string, error) {
	own, rev, err := ch.memoryIndex(ctx, mp, "Memory index of the owner")
	if err != nil {
		return "", "", err
	}
	v := struct{ Own, Project string }{Own: own}
	sign := fmt.Sprint(rev)
	if cp, ok := ch.topicProject(ctx, topic); ok && cp.ID != mp.ID {
		idx, crev, err := ch.memoryIndex(ctx, cp, "Memory index of project "+cp.Name+", which this topic is about (document_get with project "+cp.Name+")")
		if err != nil {
			return "", "", err
		}
		v.Project = idx
		sign += fmt.Sprintf("/%d:%d", cp.ID, crev)
	}
	return master.Text("owner_memory", v), sign, nil
}

// topicProject is the project a topic is about, as the list of topics names it: that of the screen of its first
// message.
func (ch *Service) topicProject(ctx context.Context, topic int64) (db.Project, bool) {
	if topic == 0 {
		return db.Project{}, false
	}
	t, err := ch.St.Q.GetTopic(ctx, topic)
	if err != nil {
		return db.Project{}, false
	}
	ts, err := ch.topics(ctx, t.WorkspaceID, false, topic, 1, func(s string) string { return s })
	if err != nil || len(ts) == 0 || ts[0].Project == "" {
		return db.Project{}, false
	}
	p, err := ch.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: t.WorkspaceID, Name: ts[0].Project})
	return p, err == nil
}

// memoryIndex is the start of the memory index of a project as a block of data with what was cut after it, and
// its revision; empty without an index.
func (ch *Service) memoryIndex(ctx context.Context, p db.Project, label string) (string, int64, error) {
	row, err := ch.St.Q.CurrentText(ctx, db.CurrentTextParams{WorkspaceID: p.WorkspaceID, ProjectID: store.ID(p.ID), Name: docs.MemoryIndex, Kind: docs.KindMemory})
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	if err != nil || strings.TrimSpace(row.Text) == "" {
		return "", row.N, err
	}
	start, cut := docs.CutIndex(row.Text)
	block := untrusted.Block(label, start)
	if cut != "" {
		block += "\n" + cut
	}
	return block, row.N, nil
}
