package untrusted

import "encoding/json"

// The kinds of a first source of text from outside.
const (
	WebFetch  = "web_fetch"
	WebSearch = "web_search"
	Network   = "network"
	Tool      = "tool"
	Token     = "token"
	File      = "file"
)

// The kinds of the data of a module text from outside passes through.
const (
	Data   = "data"
	Memory = "memory"
	Skills = "skills"
)

// From is where the text from outside a process or the data of a module holds came from, as data: its first source and
// the data of a module it last passed through. deck writes it; its subject and project are names, cut.
type From struct {
	Source Source `json:"source"`
	Via    *Via   `json:"via,omitempty"`
}

// Source is the first source: its kind and the host or name it is of.
type Source struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject,omitempty"`
}

// Via is the data of a module the text passed through: data, memory or skills, and the module.
type Via struct {
	Kind    string `json:"kind"`
	Project string `json:"project"`
}

// Of is a first source, its subject cut.
func Of(kind, subject string) From {
	return From{Source: Source{Kind: kind, Subject: Cut(subject, 120)}}
}

// Through is f after it passed through the data of a module.
func (f From) Through(kind, project string) From {
	if f.Source.Kind == "" {
		return f
	}
	f.Via = &Via{Kind: kind, Project: project}
	return f
}

// String is f as it is stored, empty for none.
func (f From) String() string {
	if f.Source.Kind == "" {
		return ""
	}
	b, _ := json.Marshal(f)
	return string(b)
}

// FromOf is the From stored as s; none for a mark written before it.
func FromOf(s string) From {
	var f From
	_ = json.Unmarshal([]byte(s), &f)
	return f
}
