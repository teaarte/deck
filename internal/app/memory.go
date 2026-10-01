package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"deck2/internal/app/docs"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// Memory is what a fresh session of a process gets of the memory: the index of its own project, as data; an
// agent of the master also gets the index of the project of the card its topic is about.
func (a *App) Memory(ctx context.Context, p db.Process) (string, error) {
	proj, err := a.Store.Q.GetProject(ctx, p.ProjectID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	own, err := a.memoryIndex(ctx, proj)
	if err != nil {
		return "", err
	}
	if proj.Builtin == 0 {
		if own == "" {
			return "", nil
		}
		fmt.Fprintf(&b, "Memory of project %s: what its owner said that holds across sessions. Read a fact with document_get "+
			"(name memory/<file>); write only what the owner said, by the memory skill when you have it.\n", proj.Name)
		b.WriteString(untrusted.Block("Memory index of project "+proj.Name, own))
		return b.String(), nil
	}
	b.WriteString("Memory of the owner: documents memory/ of project master, the index below and one document per fact " +
		"(document_get with the name). Each project keeps its own memory/ the same way; read it when you work on that project. " +
		"Keep both by the memory skill.\n")
	if own == "" {
		b.WriteString("The memory of the owner is empty so far.\n")
	} else {
		b.WriteString(untrusted.Block("Memory index of the owner", own) + "\n")
	}
	var in struct{ Topic int64 }
	_ = json.Unmarshal([]byte(p.Input), &in)
	if in.Topic == 0 {
		return b.String(), nil
	}
	t, err := a.Store.Q.GetTopic(ctx, in.Topic)
	if err != nil || t.CardID == 0 {
		return b.String(), nil
	}
	c, err := a.Store.Q.GetCard(ctx, t.CardID)
	if err != nil {
		return b.String(), nil
	}
	cp, err := a.Store.Q.GetProject(ctx, c.ProjectID)
	if err != nil || cp.ID == proj.ID {
		return b.String(), nil
	}
	if idx, err := a.memoryIndex(ctx, cp); err == nil && idx != "" {
		b.WriteString(untrusted.Block("Memory index of project "+cp.Name+", whose card this topic is about", idx) + "\n")
	}
	return b.String(), nil
}

// memoryIndex is the start of the memory index of a project, up to its limit; empty without one.
func (a *App) memoryIndex(ctx context.Context, p db.Project) (string, error) {
	text, err := docs.CurrentText(ctx, a.Store.Q, docs.ProjectScope(p), docs.KindMemory, docs.MemoryIndex)
	if err != nil || strings.TrimSpace(text) == "" {
		return "", err
	}
	return docs.CutIndex(text), nil
}
