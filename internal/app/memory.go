package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// Memory is what a fresh session of a process gets of the memory: the index of its own project, as data; an
// agent of the master gets the owner's index and, in a topic of a card, the index of the card's project.
func (a *App) Memory(ctx context.Context, p db.Process) (string, error) {
	proj, err := a.Store.Q.GetProject(ctx, p.ProjectID)
	if err != nil {
		return "", err
	}
	if proj.Builtin == 1 {
		var in struct{ Topic int64 }
		_ = json.Unmarshal([]byte(p.Input), &in)
		text, _, err := a.masterMemory(ctx, proj, in.Topic)
		return text, err
	}
	own, _, err := a.memoryIndex(ctx, proj, "Memory index of project "+proj.Name)
	if err != nil || own == "" {
		return "", err
	}
	return fmt.Sprintf("Memory of project %s: what its owner said that holds across sessions. Read a fact with document_get "+
		"(name memory/<file>); write only what the owner said, by the memory skill when you have it.\n", proj.Name) + own, nil
}

// masterMemory is the memory an agent of the master gets, with a sign that changes when the memory does.
func (a *App) masterMemory(ctx context.Context, master db.Project, topic int64) (string, string, error) {
	var b strings.Builder
	b.WriteString("Memory of the owner: documents memory/ of project master, the index below and one document per fact " +
		"(document_get with the name). Each project keeps its own memory/ the same way; read it when you work on that project. " +
		"Keep both by the memory skill.\n")
	own, rev, err := a.memoryIndex(ctx, master, "Memory index of the owner")
	if err != nil {
		return "", "", err
	}
	if own == "" {
		b.WriteString("The memory of the owner is empty so far.\n")
	} else {
		b.WriteString(own + "\n")
	}
	sign := fmt.Sprint(rev)
	if cp, ok := a.topicProject(ctx, topic); ok && cp.ID != master.ID {
		idx, crev, err := a.memoryIndex(ctx, cp, "Memory index of project "+cp.Name+", whose card this topic is about")
		if err != nil {
			return "", "", err
		}
		if idx != "" {
			b.WriteString(idx + "\n")
		}
		sign += fmt.Sprintf("/%d:%d", cp.ID, crev)
	}
	return b.String(), sign, nil
}

// topicProject is the project of the card a topic is about.
func (a *App) topicProject(ctx context.Context, topic int64) (db.Project, bool) {
	if topic == 0 {
		return db.Project{}, false
	}
	t, err := a.Store.Q.GetTopic(ctx, topic)
	if err != nil || t.CardID == 0 {
		return db.Project{}, false
	}
	c, err := a.Store.Q.GetCard(ctx, t.CardID)
	if err != nil {
		return db.Project{}, false
	}
	p, err := a.Store.Q.GetProject(ctx, c.ProjectID)
	return p, err == nil
}

// memoryIndex is the start of the memory index of a project as a block of data with what was cut after it, and
// its revision; empty without an index.
func (a *App) memoryIndex(ctx context.Context, p db.Project, label string) (string, int64, error) {
	row, err := a.Store.Q.CurrentText(ctx, db.CurrentTextParams{WorkspaceID: p.WorkspaceID, ProjectID: p.ID, Name: docs.MemoryIndex, Kind: docs.KindMemory})
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
