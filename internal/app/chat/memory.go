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
// agent of the master gets the owner's index and, in a topic of a card, the index of the card's project.
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
	v := struct{ Own, Card string }{Own: own}
	sign := fmt.Sprint(rev)
	if cp, ok := ch.topicProject(ctx, topic); ok && cp.ID != mp.ID {
		idx, crev, err := ch.memoryIndex(ctx, cp, "Memory index of project "+cp.Name+", whose card this topic is about")
		if err != nil {
			return "", "", err
		}
		v.Card = idx
		sign += fmt.Sprintf("/%d:%d", cp.ID, crev)
	}
	return master.Text("owner_memory", v), sign, nil
}

// topicProject is the project of the card a topic is about.
func (ch *Service) topicProject(ctx context.Context, topic int64) (db.Project, bool) {
	if topic == 0 {
		return db.Project{}, false
	}
	t, err := ch.St.Q.GetTopic(ctx, topic)
	if err != nil || !t.CardID.Valid {
		return db.Project{}, false
	}
	c, err := ch.St.Q.GetCard(ctx, t.CardID.Int64)
	if err != nil {
		return db.Project{}, false
	}
	p, err := ch.St.Q.GetProject(ctx, c.ProjectID)
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
