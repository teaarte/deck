package process

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// Opens is what a running process holds open: the hosts its box reaches and the CLI whose login it runs on.
type Opens struct {
	Hosts []string `json:"hosts,omitempty"`
	Login string   `json:"login,omitempty"`
}

// Mark is a small label of what a process holds open: a host with the icon of the web, or a login with the mark of
// its CLI.
type Mark struct {
	Text string `json:"text"`
	Icon string `json:"icon,omitempty"`
	Logo string `json:"logo,omitempty"`
}

// Marks are the labels of what o holds open, the login first.
func (o Opens) Marks() []Mark {
	var out []Mark
	if o.Login != "" {
		out = append(out, Mark{Text: o.Login, Logo: o.Login})
	}
	for _, h := range o.Hosts {
		out = append(out, Mark{Text: h, Icon: "world"})
	}
	return out
}

// know keeps what a process knows as it starts: what its box opens, the untrusted text a box with the network brings,
// and whether it holds something of value: a subscription login, or the data of its module.
func (r *Runner) know(ctx context.Context, id int64, hosts []string, login string, valued bool) {
	err := r.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		if len(hosts) > 0 || login != "" {
			b, _ := json.Marshal(Opens{Hosts: hosts, Login: login})
			if err := q.SetProcessOpens(ctx, db.SetProcessOpensParams{Opens: string(b), ID: id}); err != nil {
				return err
			}
		}
		if len(hosts) > 0 {
			text := fmt.Sprintf("the network of its box: %s", untrusted.Cut(strings.Join(hosts, ", "), 300))
			if err := mark(ctx, q, id, text, untrusted.Of(untrusted.Network, strings.Join(hosts, ", "))); err != nil {
				return err
			}
		}
		if !valued {
			return nil
		}
		return q.SetProcessValued(ctx, id)
	})
	if err != nil {
		slog.Error("what a process knows", "process", id, "err", err)
	}
}

// Text is what deck puts into a turn, with where the untrusted text in it came from: every door of a turn takes it.
// The owner's own word and deck's own words are a Text without From.
type Text struct {
	Body string
	From []Source
}

// Join is the bodies of ts with sep between them, trimmed, from where all of them came.
func Join(sep string, ts ...Text) Text {
	var out Text
	bodies := make([]string, 0, len(ts))
	for _, t := range ts {
		bodies, out.From = append(bodies, t.Body), append(out.From, t.From...)
	}
	out.Body = strings.TrimSpace(strings.Join(bodies, sep))
	return out
}

// Source is a piece of a turn's text by where it came from, one of the carriers below.
type Source struct {
	of carrier
	// id is the process, module or token the piece is of; topic is the topic a topic's piece is in.
	id, topic int64
}

// carrier is a kind of text deck puts into a turn that may hold text from outside, by what it says of it.
type carrier int

const (
	ofInput carrier = iota + 1
	ofWork
	ofThread
	ofTopic
	ofNote
	ofRemark
	ofTokenRemark
	ofMemory
	ofSkills
	ofWorkspaceSkills
	carrierEnd
)

// carriers say where the text of a source came from: of a process its id and what it read, of a module its name and
// who wrote there, of a token its name; the first %d of a topic's is the topic.
var carriers = map[carrier]string{
	ofInput:           "the input of process %d, which read %s",
	ofWork:            "the work it continues of process %d, which read %s",
	ofThread:          "the result of process %d of its record, which read %s",
	ofTopic:           "topic %d: process %d, which read %s",
	ofNote:            "a note of deck that quotes process %d, which read %s",
	ofRemark:          "a remark of process %d, which read %s",
	ofTokenRemark:     "a remark of automation token %s",
	ofMemory:          "the memory of %s, where %s wrote",
	ofSkills:          "the skills of %s, where %s wrote",
	ofWorkspaceSkills: "the skills of the workspace in %s, where %s wrote",
}

// Carriers are the kinds of text a turn is given that carry the mark of where they came from, by what deck says of it.
func Carriers() []string {
	out := make([]string, 0, len(carriers))
	for c := ofInput; c < carrierEnd; c++ {
		out = append(out, carriers[c])
	}
	return out
}

func inputOf(process int64) Source  { return Source{of: ofInput, id: process} }
func workOf(process int64) Source   { return Source{of: ofWork, id: process} }
func threadOf(process int64) Source { return Source{of: ofThread, id: process} }

// InTopic is the words of a process a topic holds.
func InTopic(topic, process int64) Source { return Source{of: ofTopic, id: process, topic: topic} }

// NoteOf is a note of deck that quotes a process.
func NoteOf(process int64) Source { return Source{of: ofNote, id: process} }

// RemarkOf is a remark of a process, or of an automation token from outside deck (registry.Actor.Outside).
func RemarkOf(process, token int64) Source {
	if token != 0 {
		return Source{of: ofTokenRemark, id: token}
	}
	return Source{of: ofRemark, id: process}
}

// MemoryOf is the memory of a module.
func MemoryOf(project int64) Source { return Source{of: ofMemory, id: project} }

// SkillsOf is the skills of a module.
func SkillsOf(project int64) Source { return Source{of: ofSkills, id: project} }

// WorkspaceSkillsIn is the skills of the workspace, whose mark the master holds.
func WorkspaceSkillsIn(master int64) Source { return Source{of: ofWorkspaceSkills, id: master} }

// carried is where the untrusted text of the first of sources that holds some came from, in words and as data, empty for
// none: the words of a topic first, then the input of a parent, then the work a process continues, then the rest.
func carried(ctx context.Context, q *db.Queries, sources ...Source) (string, untrusted.From) {
	sources = slices.Clone(sources)
	slices.SortStableFunc(sources, func(a, b Source) int { return cmp.Compare(rank(a.of), rank(b.of)) })
	for _, s := range sources {
		if s.id == 0 {
			continue
		}
		switch s.of {
		case ofTokenRemark:
			if t, err := q.GetToken(ctx, s.id); err == nil {
				return fmt.Sprintf(carriers[s.of], t.Name), untrusted.Of(untrusted.Token, t.Name)
			}
		case ofMemory, ofSkills, ofWorkspaceSkills:
			if p, err := q.GetProject(ctx, s.id); err == nil && p.Untrusted != "" {
				via := map[carrier]string{ofMemory: untrusted.Memory, ofSkills: untrusted.Skills, ofWorkspaceSkills: untrusted.Skills}[s.of]
				return fmt.Sprintf(carriers[s.of], p.Name, p.Untrusted), untrusted.FromOf(p.ReadFrom).Through(via, p.Name)
			}
		case ofTopic:
			if p, err := q.GetProcess(ctx, s.id); err == nil && p.ReadUntrusted != "" {
				return fmt.Sprintf(carriers[s.of], s.topic, p.ID, p.ReadUntrusted), untrusted.FromOf(p.ReadFrom)
			}
		default:
			if p, err := q.GetProcess(ctx, s.id); err == nil && p.ReadUntrusted != "" {
				return fmt.Sprintf(carriers[s.of], p.ID, p.ReadUntrusted), untrusted.FromOf(p.ReadFrom)
			}
		}
	}
	return "", untrusted.From{}
}

// rank is the order in which carried tells the sources of a process apart.
func rank(c carrier) int {
	switch c {
	case ofTopic:
		return 0
	case ofInput:
		return 1
	case ofWork:
		return 2
	}
	return 3
}

// carries marks a process within a transaction by what sources hold.
func carries(ctx context.Context, q *db.Queries, id int64, sources ...Source) error {
	text, from := carried(ctx, q, sources...)
	return mark(ctx, q, id, untrusted.Cut(text, 500), from)
}

// gives marks a process by what the texts it is given carry, in a transaction of its own.
func (r *Runner) gives(ctx context.Context, id int64, ts ...Text) {
	var from []Source
	for _, t := range ts {
		from = append(from, t.From...)
	}
	if len(from) == 0 {
		return
	}
	err := r.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error {
		return carries(ctx, q, id, from...)
	})
	if err != nil {
		slog.Error("mark a process", "process", id, "err", err)
	}
}

// given is the files of skills a turn gets, the process marked by where they came from.
func (r *Runner) given(ctx context.Context, id int64, files map[string]Text) map[string]string {
	out := make(map[string]string, len(files))
	var from []Source
	for name, t := range files {
		out[name] = t.Body
		from = append(from, t.From...)
	}
	// The module's own skills say where they came from before the workspace's.
	slices.SortStableFunc(from, func(a, b Source) int { return cmp.Compare(a.of, b.of) })
	r.gives(ctx, id, Text{From: from})
	return out
}

// mark is the one writer of the mark of a process: where the untrusted text it read came from, in words and as data;
// the first stays.
func mark(ctx context.Context, q *db.Queries, id int64, text string, from untrusted.From) error {
	if text == "" {
		return nil
	}
	return q.MarkProcessRead(ctx, db.MarkProcessReadParams{ReadUntrusted: text, ReadFrom: from.String(), ID: id})
}
