// Package surface keeps the snapshot of what deck shows modules, agents and automations: every command with its
// input, result, topics and gate, the tools of a turn by role, the rights of the starters and the master.
package surface

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"

	"deck2/internal/provider"
	"deck2/internal/registry"
	shipped "deck2/starters"
	"deck2/tests/harness"
)

var update = flag.Bool("update", false, "write testdata/surface.json anew from this deck")

const golden = "testdata/surface.json"

func TestMain(m *testing.M) { harness.Main(m) }

// TestTheSurfaceIsTheSnapshot: what deck shows outside is the snapshot; a change of it is either named in its
// commit (go test ./tests/surface -update, and a line in master/changes.md when modules see it) or a bug.
func TestTheSurfaceIsTheSnapshot(t *testing.T) {
	got := take(t)
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("deck's surface is not the snapshot; if the change is meant, run go test ./tests/surface -update and name it in the commit:\n%s", diff(want, got))
	}
}

// TestTheSnapshotIsTheSameTwice: two decks set up alike give the same snapshot to the byte.
func TestTheSnapshotIsTheSameTwice(t *testing.T) {
	a, b := take(t), take(t)
	if !bytes.Equal(a, b) {
		t.Fatalf("the snapshot differs between two decks:\n%s", diff(a, b))
	}
}

type surface struct {
	Commands []command           `json:"commands"`
	Calls    map[string][]string `json:"calls"`
	Turns    map[string][]string `json:"turns"`
	Rights   map[string][]right  `json:"rights"`
}

type command struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Tool        string          `json:"tool"`
	Callers     []string        `json:"callers"`
	Input       json.RawMessage `json:"input"`
	Result      json.RawMessage `json:"result,omitempty"`
	Topics      []string        `json:"topics,omitempty"`
	Traits      []string        `json:"traits,omitempty"`
	Gate        gate            `json:"gate"`
}

type gate struct {
	Gated  bool   `json:"gated,omitempty"`
	About  bool   `json:"about,omitempty"`
	Undo   bool   `json:"undo,omitempty"`
	Term   string `json:"term,omitempty"`
	Brings string `json:"brings,omitempty"`
}

type right struct {
	Right    string `json:"right"`
	Who      string `json:"who,omitempty"`
	Subject  string `json:"subject,omitempty"`
	Answer   string `json:"answer,omitempty"`
	Declared bool   `json:"declared,omitempty"`
	Key      bool   `json:"key,omitempty"`
}

// take is the snapshot of a deck with a working week behind it, a turn of a module and of the master in it and the
// starter imported.
func take(t *testing.T) []byte {
	t.Helper()
	var mu sync.Mutex
	listed := map[int64][]string{}
	agent := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		names := harness.ToolNames(c.Session)
		mu.Lock()
		listed[c.Req.Project] = slices.Sorted(slices.Values(names))
		mu.Unlock()
		if c.Req.ResultSchema != nil {
			return provider.Result{Structured: json.RawMessage(`[]`)}
		}
		return provider.Result{Text: "ok"}
	}}
	e := harness.LightEnv(t, harness.WithProvider(agent))
	turn := e.WaitState(e.Start("notes", "score", map[string]any{"title": "a"}), "done", "error")
	if turn.State != "done" {
		t.Fatalf("the turn of the module: %+v", turn)
	}
	dirs, err := shipped.FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var starters []string
	for _, d := range dirs {
		starters = append(starters, d.Name())
		e.Must("deck.project_import", map[string]any{"starter": d.Name()}, nil)
	}
	// The commands of deck's browser come from its server, which deck starts beside.
	e.Until("the commands of the browser", func() bool {
		_, err := e.App.Registry.Lookup(context.Background(), 1, "deck.browser_navigate")
		return err == nil
	})

	var s surface
	ctx := context.Background()
	var all []struct {
		ID   int64
		Name string
	}
	e.Must("deck.projects_list", nil, &all)
	project, id := map[int64]string{}, map[string]int64{}
	for _, p := range all {
		project[p.ID], id[p.Name] = p.Name, p.ID
	}
	master, notes := id["master"], id["notes"]
	seen := map[string]bool{}
	s.Calls = map[string][]string{}
	for _, who := range []struct {
		role string
		a    registry.Actor
	}{
		{"owner", registry.Actor{Kind: registry.Owner, Workspace: 1}},
		{"master", registry.Actor{Kind: registry.Master, Workspace: 1, ProjectID: master}},
		{"agent", registry.Actor{Kind: registry.Agent, Workspace: 1, ProjectID: notes}},
		{"agent trial", registry.Actor{Kind: registry.Agent, Workspace: 1, ProjectID: notes, Trial: true}},
		{"trigger", registry.Actor{Kind: registry.Trigger, Workspace: 1, ProjectID: notes}},
		{"token", registry.Actor{Kind: registry.Token, Workspace: 1, Commands: []string{"*"}}},
	} {
		cmds, err := e.App.Registry.List(ctx, who.a)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cmds {
			s.Calls[who.role] = append(s.Calls[who.role], c.Name)
			if seen[c.Name] {
				continue
			}
			seen[c.Name] = true
			cmd := command{Name: c.Name, Title: c.Title, Description: c.Description, Tool: c.ToolDescription(), Callers: c.Callers, Input: compact(t, c.Input),
				Result: compact(t, c.Result), Topics: c.Topics, Traits: c.Traits(),
				Gate: gate{Gated: c.Gated(), About: c.Gate.Subject != nil, Undo: c.Gate.Undo != nil, Brings: string(c.Gate.Brings)}}
			if c.Gate.Term != 0 {
				cmd.Gate.Term = c.Gate.Term.String()
			}
			s.Commands = append(s.Commands, cmd)
		}
	}
	slices.SortFunc(s.Commands, func(a, b command) int { return strings.Compare(a.Name, b.Name) })

	s.Turns = map[string][]string{}
	for _, c := range agent.Calls() {
		s.Turns["allowed in a turn of "+project[c.Project]] = slices.Sorted(slices.Values(c.AllowedTools))
	}
	mu.Lock()
	for p, names := range listed {
		s.Turns["listed in a turn of "+project[p]] = names
	}
	mu.Unlock()
	if len(s.Turns) != 4 {
		t.Fatalf("the tools of a turn of the module and of the master: %v", s.Turns)
	}
	_, tok := e.Token("surface", "*")
	terminal, err := e.Terminal(tok)
	if err != nil {
		t.Fatal(err)
	}
	s.Turns["listed to a token"] = slices.Sorted(slices.Values(harness.ToolNames(terminal)))

	s.Rights = map[string][]right{}
	names := []string{"master", "notes"}
	names = append(names, starters...)
	for _, p := range names {
		var rows []right
		e.Must("deck.rights_list", map[string]any{"project": p}, &rows)
		s.Rights[p] = rows
	}
	out, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(ascii(out), '\n')
}

// ascii escapes the letters past ASCII deck's descriptions quote, so the snapshot is English to the check of words.
func ascii(b []byte) []byte {
	var out strings.Builder
	for _, r := range string(b) {
		if r < 0x80 {
			out.WriteRune(r)
			continue
		}
		for _, u := range utf16.Encode([]rune{r}) {
			fmt.Fprintf(&out, "\\u%04x", u)
		}
	}
	return []byte(out.String())
}

func compact(t *testing.T, raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// diff names the first lines where want and got part, a few around them.
func diff(want, got []byte) string {
	w, g := strings.Split(string(want), "\n"), strings.Split(string(got), "\n")
	var out []string
	for i := 0; i < max(len(w), len(g)) && len(out) < 40; i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			out = append(out, fmt.Sprintf("%d\n- %s\n+ %s", i+1, a, b))
		}
	}
	return strings.Join(out, "\n")
}
