package harness

import (
	"strings"
)

func (e *Env) HealthRowOf(subject string) map[string]any {
	var hs []map[string]any
	e.Must("deck.health_list", nil, &hs)
	for _, h := range hs {
		if h["subject"] == subject {
			return h
		}
	}
	return nil
}

// HealthSeen is whether the owner read the failing check of a subject in a project.
func (e *Env) HealthSeen(project, subject string) (bool, bool) {
	e.T.Helper()
	var hs []struct {
		Subject, Project, Status string
		Seen                     bool
	}
	e.Must("deck.health_list", nil, &hs)
	for _, h := range hs {
		if h.Subject == subject && h.Project == project {
			return h.Seen, h.Status == "error"
		}
	}
	return false, false
}

// HealthRow is a row of deck's health.
type HealthRow struct {
	Subject string `json:"subject"`
	Status  string `json:"status"`
	Error   string `json:"error"`
	Version string `json:"version"`
}

// Health is deck's health, of a project when one is named.
func (e *Env) Health(project ...string) []HealthRow {
	e.T.Helper()
	in := map[string]any{}
	if len(project) > 0 {
		in["project"] = project[0]
	}
	var hs []HealthRow
	e.Must("deck.health_list", in, &hs)
	return hs
}

// WaitHealth waits until the health of subject is status, or any status for "", and returns it.
func (e *Env) WaitHealth(subject, status string) HealthRow {
	e.T.Helper()
	var got HealthRow
	e.Until("health of "+subject+" to be "+status, func() bool {
		for _, h := range e.Health() {
			if h.Subject == subject && (status == "" || h.Status == status) {
				got = h
				return true
			}
		}
		return false
	})
	return got
}

func (e *Env) Warnings(contains string) int {
	e.T.Helper()
	n := 0
	for _, f := range e.Notes() {
		if f.Author == "deck" && !f.Read && strings.Contains(f.Text, contains) {
			n++
		}
	}
	return n
}

// feedNote is a note of the owner's feed.
type Note struct {
	ID           int64
	Author, Text string
	CreatedAt    string `json:"created_at"`
	Read         bool
	CanRead      bool `json:"can_read"`
}

// notes are the notes of the owner's feed, newest first.
func (e *Env) Notes() []Note {
	e.T.Helper()
	var out []Note
	e.Must("deck.feed_list", map[string]any{"kind": "note"}, &out)
	return out
}

// waitNote waits for an unread note of deck that holds contains.
func (e *Env) WaitNote(contains string) Note {
	e.T.Helper()
	var got Note
	e.Until("a note of deck with "+contains, func() bool {
		for _, f := range e.Notes() {
			if f.Author == "deck" && !f.Read && strings.Contains(f.Text, contains) {
				got = f
				return true
			}
		}
		return false
	})
	return got
}
