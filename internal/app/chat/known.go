package chat

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"deck2/internal/access"
	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

const (
	// indexMax is the most the project index of a reply turn takes, openMax its open work.
	indexMax = 2048
	openMax  = 1024
	// openLook is how many of the newest processes of a topic a turn looks at for its open work.
	openLook = 50
)

// projectIndex is the projects of a workspace, one a line with its state and the first line of its description, as
// data; empty without any.
func (s *Service) projectIndex(ctx context.Context, workspace int64) string {
	ps, err := s.St.Q.ListWorkspaceProjects(ctx, workspace)
	if err != nil {
		return ""
	}
	lines := make([]string, 0, len(ps))
	for _, p := range ps {
		if p.Builtin == 1 {
			continue
		}
		state := p.Status
		if p.Paused == 1 {
			state += ", paused"
		}
		line := fmt.Sprintf("- %s (%s)", p.Name, state)
		if prompt, err := docs.CurrentText(ctx, s.St.Q, docs.ProjectScope(p), docs.KindPrompt, docs.PromptName); err == nil {
			if d := untrusted.Flat(docs.Description(prompt), 120); d != "" {
				line += ": " + d
			}
		}
		lines = append(lines, line)
	}
	return within("Projects of deck", lines, indexMax, "projects_list gives them all")
}

// openWork is what the replies of a topic started and still goes, with what of it waits for the owner, one a line, as
// data; empty when nothing does.
func (s *Service) openWork(ctx context.Context, t db.ChatTopic) string {
	m, err := access.Master(ctx, s.St.Q, t.WorkspaceID)
	if err != nil {
		return ""
	}
	rows, err := s.St.Q.ListTopicProcesses(ctx, db.ListTopicProcessesParams{MasterID: m.ID, Topic: t.ID, Lim: openLook})
	if err != nil {
		return ""
	}
	var lines []string
	for _, r := range rows {
		p := r.Process
		if slices.Contains(process.Final, p.State) {
			continue
		}
		lines = append(lines, fmt.Sprintf("- #%d %s.%s, %s since %s", p.ID, r.Project, p.Command, p.State, p.StartedAt))
		asks, _ := s.St.Q.ListOpenAsksOfProcess(ctx, p.ID)
		for _, a := range asks {
			if a.State != ask.StateOpen {
				continue
			}
			lines = append(lines, fmt.Sprintf("  - ask #%d %s waits for the owner since %s: %s", a.ID, a.Kind, a.CreatedAt, untrusted.Flat(a.Text, 120)))
		}
	}
	return within("Open work of this topic, newest first", lines, openMax, registry.Deck("processes_list")+" with topic gives it all")
}

// within is lines as one block of data of at most limit bytes of whole lines, with how many it left out; empty without
// lines.
func within(label string, lines []string, limit int, rest string) string {
	if len(lines) == 0 {
		return ""
	}
	size := 0
	for i, l := range lines {
		if size += len(l) + 1; size > limit && i > 0 {
			return untrusted.Block(label, strings.Join(lines[:i], "\n")) +
				fmt.Sprintf("\ndeck cut this list at %d of its %d lines; %s.", i, len(lines), rest)
		}
	}
	return untrusted.Block(label, strings.Join(lines, "\n"))
}
