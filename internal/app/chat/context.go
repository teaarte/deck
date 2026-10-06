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
	Process int64  `json:"process,omitempty"`
	// Files are the stored files the owner attached to the message.
	Files fileIDs `json:"files,omitempty"`
}

// fileIDs are the ids of attached files; a path, as older messages kept them, is skipped.
type fileIDs []int64

func (f *fileIDs) UnmarshalJSON(b []byte) error {
	var all []json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return err
	}
	*f = nil
	for _, x := range all {
		var id int64
		if json.Unmarshal(x, &id) == nil {
			*f = append(*f, id)
		}
	}
	return nil
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
	}{{"record", s.Record}, {"process", s.Process}} {
		if f.id != 0 {
			parts = append(parts, fmt.Sprintf("%s %d", f.name, f.id))
		}
	}
	return strings.Join(parts, ", ")
}
