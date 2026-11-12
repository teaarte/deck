package store

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func Test_rights16(t *testing.T) {
	agent := func(provider string) string {
		return fmt.Sprintf(`{"name": "a", "title": "A", "description": "d", "class": "internal", "provider": %q, "executor": {"type": "agent", "prompt": "p"}}`, provider)
	}
	tool := func(name, method, class, extra string) string {
		return fmt.Sprintf(`{"name": %q, "description": "d", "method": %q, "url": "https://api.example/x", "class": %q%s}`, name, method, class, extra)
	}
	for _, row := range []struct {
		manifest string
		want     []string
		what     string
	}{
		{`"domains": {"read": ["A.example", "*.b.example"], "act": ["shop.example"]}`,
			[]string{"network programs a.example +", "network programs b.example +", "network programs shop.example +", "send  shop.example +"},
			"domains read and act: the network of the programs, the sites of act also sending"},
		{`"commands": [` + agent("claude-code") + `], "domains": {"read": ["x.example"]}`,
			[]string{"network programs x.example +", "login  claude-code +"}, "an agent on a login: the login, no network in its shell"},
		{`"commands": [` + agent("") + `]`, []string{"login  claude-code +", "login  codex +"}, "an agent on the main provider may run on either login"},
		{`"commands": [` + agent("free") + `]`, []string{"login  claude-code +", "login  codex +"}, "free models first may fall back to a login"},
		{`"commands": [` + agent("codex@Mac") + `]`, []string{"login  codex +"}, "a login on a node"},
		{`"commands": [` + agent("openrouter-x") + `], "domains": {"read": ["x.example"]}`,
			[]string{"network programs x.example +", "network agent x.example +", "network agent pypi.org +", "network agent files.pythonhosted.org +",
				"network agent registry.npmjs.org +", "network agent proxy.golang.org +", "network agent sum.golang.org +", "network agent github.com +",
				"network agent codeload.github.com +", "network agent objects.githubusercontent.com +", "network agent huggingface.co +", "network agent hf.co +"},
			"an agent off a login keeps the network of its shell, with the hosts it reached by default"},
		{`"commands": [` + agent("openrouter-x") + `]`, nil, "an agent off a login without domains reaches nothing"},
		{`"reads": [{"project": "*", "models": true, "runs": true}, {"project": "notes", "collections": ["items"], "documents": ["plan.md"]}, {"project": "dec"}, {"project": "tasks", "runs": true}]`,
			[]string{"data  *:models +", "data  *:runs +", "data  notes.items +", "data  notes/plan.md +", "data  dec +", "data  tasks:runs +"}, "reads: the data of modules"},
		{`"gpu": true, "models": ["openrouter", "@image"], "commands": [{"name": "m", "title": "M", "description": "d", "class": "internal", "executor": {"type": "model", "kind": "tts"}},
			{"name": "a", "title": "A", "description": "d", "class": "internal", "provider": "openrouter-x", "executor": {"type": "agent", "prompt": "p", "folder": "photos"}},
			{"name": "b", "title": "B", "description": "d", "class": "internal", "provider": "openrouter-x", "executor": {"type": "agent", "prompt": "p", "folder": true}}]`,
			[]string{"files  gpu +", "files  photos +", "files  * +", "money  openrouter +", "money  @image +", "money  @tts +"}, "gpu, folders and models"},
		{`"allowed_external": ["post", "deck.model_set"], "commands": [{"name": "post", "title": "P", "description": "d", "class": "external", "executor": {"type": "script", "script": "s"}},
			{"name": "mail", "title": "M", "description": "d", "class": "external", "executor": {"type": "tool", "tool": "t"}},
			{"name": "undo", "title": "U", "description": "d", "class": "reversible", "executor": {"type": "script", "script": "s"}}]`,
			[]string{"send  post +", "send  mail -", "send  undo +", "send  deck.model_set +"}, "classes: external asks unless allowed, reversible ran"},
		{`"allowed_external": ["put"], "tools": {"http": [` + tool("get", "GET", "internal", "") + `, ` + tool("key", "GET", "external", `, "headers": {"K": "$secret.k"}`) + `, ` +
			tool("put", "PUT", "external", "") + `, ` + tool("post", "POST", "internal", "") + `, ` + tool("del", "DELETE", "external", "") + `]}`,
			[]string{"network programs api.example +", "data  key -", "send  put +", "send  post +", "send  del -"}, "HTTP tools by method, key and class"},
		{`"allowed_external": ["srv.go"], "tools": {"mcp": {"servers": {"srv": {"command": "x", "classes": {"look": "internal", "go": "external", "hop": "reversible", "no": "external"}}}}}`,
			[]string{"send  srv.go +", "send  srv.hop +", "send  srv.no -"}, "MCP tools by class and the prints the owner approved"},
	} {
		text := `{"version": 16, ` + row.manifest + `}`
		got, ok := rights16(text, map[string]string{"srv.go": "p1", "srv.hop": "p2"})
		if !ok {
			t.Errorf("%s: not read as format 16", row.what)
			continue
		}
		var lines []string
		for _, d := range got {
			mark := "-"
			if d.works {
				mark = "+"
			}
			lines = append(lines, strings.Join([]string{d.right, d.who, d.subject, mark}, " "))
		}
		slices.Sort(lines)
		want := slices.Clone(row.want)
		slices.Sort(want)
		if !slices.Equal(lines, want) {
			t.Errorf("%s:\n got %q\nwant %q", row.what, lines, want)
		}
	}
	if _, ok := rights16(`{"version": 17}`, nil); ok {
		t.Errorf("a manifest of format 17 is no format 16")
	}
}
