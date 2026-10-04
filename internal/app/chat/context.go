package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"deck2/internal/untrusted"
)

// screenContext is the screen of the owner a message was written on.
type screenContext struct {
	Project string `json:"project,omitempty"`
	Screen  string `json:"screen,omitempty"`
	Record  int64  `json:"record,omitempty"`
	Card    int64  `json:"card,omitempty"`
	Process int64  `json:"process,omitempty"`
	// Files are the stored files the owner attached to the message.
	Files []int64 `json:"files,omitempty"`
}

func screenText(raw string) string {
	var s screenContext
	if raw == "" || json.Unmarshal([]byte(raw), &s) != nil {
		return ""
	}
	var parts []string
	if s.Project != "" {
		parts = append(parts, "project "+untrusted.Line(s.Project, 60))
	}
	if s.Screen != "" {
		parts = append(parts, "screen "+untrusted.Line(s.Screen, 60))
	}
	for _, f := range []struct {
		name string
		id   int64
	}{{"record", s.Record}, {"card", s.Card}, {"process", s.Process}} {
		if f.id != 0 {
			parts = append(parts, fmt.Sprintf("%s %d", f.name, f.id))
		}
	}
	return strings.Join(parts, ", ")
}
