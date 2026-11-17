package server

import (
	"context"
	"slices"
	"strings"
	"testing"

	"deck2/internal/app"
	"deck2/internal/words"
	"deck2/tests/harness"
)

// TestALocalConnectionWithoutANodeInDecksImageSaysItReachesTheContainer: deck in its image warns of a local connection
// on 127.0.0.1 without a node, not of one on the local network; a fix clears it; deck outside its image says nothing.
func TestALocalConnectionWithoutANodeInDecksImageSaysItReachesTheContainer(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithConfig(func(c *app.Config) { c.Image = "ghcr.io/teaarte/deck" }))
	e.Must("deck.connection_set", map[string]any{"name": "media", "url": "http://127.0.0.1:8190/v1", "local": true}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "lan", "url": "http://192.168.1.20:11434/v1", "local": true}, nil)
	row := e.WaitHealth("connection:media", "warning")
	if !strings.Contains(row.Error, "this address is the container itself") || !strings.Contains(row.Error, "add a node") {
		t.Fatalf("the row says the address is the container and what to do: %+v", row)
	}
	var ru []struct{ Subject, Error string }
	e.In("ru").Must("deck.health_list", nil, &ru)
	if i := slices.IndexFunc(ru, func(r struct{ Subject, Error string }) bool { return r.Subject == "connection:media" }); i < 0 || ru[i].Error != words.In("ru")[row.Error] || ru[i].Error == "" {
		t.Fatalf("the row speaks the owner's language: %+v", ru)
	}
	e.Must("deck.connection_set", map[string]any{"name": "media", "url": "http://192.168.1.20:8190/v1", "local": true}, nil)
	e.Until("the row goes once the address is fixed", func() bool { return e.HealthRowOf("connection:media") == nil })
	e.App.Health.Derive(context.Background(), 0, nil)
	if e.HealthRowOf("connection:lan") != nil {
		t.Fatal("a connection on the local network reaches its machine from the container")
	}

	out := harness.New(t)
	out.Must("deck.connection_set", map[string]any{"name": "media", "url": "http://127.0.0.1:8190/v1", "local": true}, nil)
	out.App.Health.Derive(context.Background(), 0, nil)
	if out.HealthRowOf("connection:media") != nil {
		t.Fatal("deck outside its image reaches the owner's machine on 127.0.0.1")
	}
}

// TestTodayLeadsToAccessWhileNoModelCanWorkAsAnAgent: while every agent is signed out or missing, the first line of
// Today says so and opens Settings → Models; with one that works, the line is gone.
func TestTodayLeadsToAccessWhileNoModelCanWorkAsAnAgent(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	type line struct {
		ID, Kind, Title, Text string
		Actions               []map[string]any
	}
	access := func() *line {
		var lines []line
		e.Must("deck.today_get", nil, &lines)
		if len(lines) > 0 && lines[0].ID == "access" {
			return &lines[0]
		}
		if slices.ContainsFunc(lines, func(l line) bool { return l.ID == "access" }) {
			t.Fatalf("the line of access comes first: %+v", lines)
		}
		return nil
	}
	if l := access(); l != nil {
		t.Fatalf("with an agent that works there is no line of access: %+v", l)
	}
	var models struct {
		Models []struct{ Name, Agent string }
	}
	e.Must("deck.models_list", nil, &models)
	var agents []string
	for _, m := range models.Models {
		if m.Agent != "" {
			agents = append(agents, m.Name)
		}
	}
	// The self-checks of the agents of the test say what a CLI signed out and one not installed say.
	for i, name := range agents {
		e.SQL(`INSERT INTO health (subject, status, error) VALUES (?, ?, 'not signed in')`, "provider:"+name, []string{"needs_login", "missing"}[i%2])
	}
	l := access()
	if len(agents) == 0 || l == nil || l.Title != "No model can work as an agent yet" || !strings.Contains(l.Text, "OpenRouter") ||
		len(l.Actions) != 1 || l.Actions[0]["screen"] != "settings" {
		t.Fatalf("the first line of Today leads to access while %v work in none: %+v", agents, l)
	}
	var ru []line
	e.In("ru").Must("deck.today_get", nil, &ru)
	if len(ru) == 0 || ru[0].Title != words.In("ru")[l.Title] || ru[0].Title == "" {
		t.Fatalf("the line speaks the owner's language: %+v", ru)
	}
	// The self-check of the first agent says it works again.
	e.SQL(`UPDATE health SET status = 'ok' WHERE subject = ?`, "provider:"+agents[0])
	if l := access(); l != nil {
		t.Fatalf("an agent that works again takes the line away: %+v", l)
	}
}
