package process

import (
	"deck2/internal/manifest"
	"fmt"
	"slices"
	"strings"
)

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// buildPrompt mentions only the tools the provider session really has.
func buildPrompt(project, instructions, body string, m *manifest.Manifest, cmd *manifest.Command, input string, tools []string, questionTool string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are running command %q of deck project %q.\n\n", cmd.Name, project)
	if strings.TrimSpace(instructions) != "" {
		fmt.Fprintf(&b, "Project instructions:\n%s\n\nCommand instructions:\n", strings.TrimSpace(instructions))
	}
	b.WriteString(body)
	fmt.Fprintf(&b, "\n\nInput: %s\n", input)
	if len(m.Collections) > 0 && slices.Contains(tools, "records_upsert") {
		b.WriteString("\nCollections of this project (write with the records_upsert tool of the deck MCP server, records as JSONL):\n")
		for _, c := range m.Collections {
			fmt.Fprintf(&b, "- %s: %s. Record JSON schema: %s\n", c.Name, c.Description, string(c.Schema))
		}
	}
	if slices.Contains(tools, "result_submit") {
		b.WriteString("\nHand in the final result by calling the result_submit tool of the deck MCP server once, then end your turn." +
			" When there is nothing to do, hand in null.\n")
	} else if len(cmd.Result) > 0 {
		b.WriteString("\nWhen there is nothing to do, the result is null.\n")
	}
	if questionTool != "" {
		fmt.Fprintf(&b, "\nIf you need a decision from the owner, ask with %s.\n", questionTool)
	}
	return b.String()
}
