package chat

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"deck2/internal/gateway"
	"deck2/internal/provider"
	"deck2/internal/registry"
)

// agentTools are what the agent's own tools and the tools of deck that are no command do, in a phrase, and the kind of work each is.
var agentTools = map[string][2]string{
	"Bash": {"runs a command", "work"}, "Read": {"reads a file", "read"}, "Grep": {"searches the files", "read"},
	"Glob": {"searches the files", "read"}, "Edit": {"edits a file", "work"}, "Write": {"writes a file", "work"},
	"NotebookEdit": {"edits a file", "work"}, "WebFetch": {"reads a page of the web", "search"},
	"WebSearch": {"searches the web", "search"}, "ToolSearch": {"looks for a tool", "read"}, "Skill": {"reads a skill", "read"},
	"Task": {"gives a part to a helper", "work"}, "Agent": {"gives a part to a helper", "work"}, "TodoWrite": {"plans the work", "work"},
	"AskUserQuestion": {"asks you", "work"}, gateway.AskTool: {"asks you", "work"}, gateway.SubmitTool: {"gives its result", "work"},
}

var (
	searches = regexp.MustCompile(`(?i)web_?search|web_?fetch|browser|search`)
	draws    = regexp.MustCompile(`(?i)draw|cover|theme_image|image|model_call`)
)

// doing is what a call of a turn does in the owner's words and the kind of work it is: read, search, draw or work.
func (ch *Service) doing(ctx context.Context, workspace int64, title string, tr func(string) string) (string, string) {
	tool := strings.TrimPrefix(title, provider.Qualify(""))
	if t, ok := agentTools[tool]; ok {
		return tr(t[0]), t[1]
	}
	act := "work"
	switch {
	case searches.MatchString(tool):
		act = "search"
	case draws.MatchString(tool):
		act = "draw"
	}
	name := registry.CommandName(tool)
	c, err := ch.Lookup(ctx, workspace, name)
	if err != nil || name == "" {
		return title, act
	}
	if c.Reads && act == "work" {
		act = "read"
	}
	if c.ProjectID != 0 {
		return fmt.Sprintf(tr("calls %s"), c.Name), act
	}
	return fmt.Sprintf(tr("calls «%s»"), tr(c.Title)), act
}
