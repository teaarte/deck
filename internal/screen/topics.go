package screen

import (
	"maps"
	"slices"
	"strings"

	"deck2/internal/manifest"
)

// follows reports whether the block is read again after an event of the topic.
func follows(b manifest.Block, deck bool, topic string) bool {
	return slices.Contains(topicsOf(b, deck), topic)
}

// reads says whether a block of the screen reads a part of a project.
func (r *renderer) reads(b manifest.Block, project string) bool {
	return readsProject(b, r.deck(), r.req.Project.Name, project)
}

// readsProject says whether a block of a screen of project own reads project: a deck screen reads every one, a
// command the project its input names, every one for an empty or computed name or when it takes no project.
func readsProject(b manifest.Block, deck bool, own, project string) bool {
	switch {
	case project == "" || deck:
		return true
	case b.Source == nil:
		return project == own
	}
	if v, ok := b.Source.Input["project"]; ok {
		s, _ := v.(string)
		return s == "" || strings.HasPrefix(s, "=") || s == project
	}
	if c, ok := manifest.Reads(b.Source.From); ok && manifest.FieldTypes(c.Input)["project"] == "" {
		return true
	}
	return project == own
}

// topicsOf are the event topics after which the block is read again: those of the reads it makes.
func topicsOf(b manifest.Block, deck bool) []string {
	switch {
	case b.Type == "appearance", b.Type == "actions" && deck:
		return nil
	case b.Type == "document":
		return readTopics(documentRead)
	case b.Type == "custom" && b.Source != nil:
		return union(readTopics(documentRead), sourceTopics(*b.Source))
	case b.Type == "custom":
		return readTopics(documentRead)
	case b.Source == nil:
		return readTopics(pageRead(deck && b.Type != "form", ""))
	case b.Type == "chat":
		out := sourceTopics(*b.Source)
		for _, part := range slices.Sorted(maps.Values(manifest.ChatParts)) {
			out = union(out, readTopics(part))
		}
		return out
	}
	return sourceTopics(*b.Source)
}

// sourceTopics are the topics of the read of a source; a collection is read by records_list.
func sourceTopics(src manifest.Source) []string {
	if src.From == "" || src.From == "collection" {
		return readTopics(recordsRead)
	}
	return readTopics(src.From)
}

// The reads of a screen deck makes itself: the record of a page, a document and the records of a collection.
const (
	processRead  = "deck.process_get"
	documentRead = "deck.document_get"
	recordRead   = "deck.record_get"
	recordsRead  = "deck.records_list"
)

// pageRead is the read of the record of a page: a process on a screen of deck, a document, else a record.
func pageRead(deck bool, record string) string {
	switch {
	case deck:
		return processRead
	case record == manifest.DocumentRecord:
		return documentRead
	}
	return recordRead
}

func readTopics(command string) []string {
	rc, _ := manifest.Reads(command)
	return rc.Topics
}

func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, t := range b {
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// changesWhen reports whether an event of the topic in project changed may put a block of the screen of project
// own on it or take it off: its when reads its source or the record of the page. The screen is then resolved whole.
func changesWhen(bs []manifest.Block, record string, deck bool, own, changed, topic string) bool {
	page := readTopics(pageRead(deck, record))
	for _, b := range bs {
		switch {
		case b.When != "" && record != "" && slices.Contains(page, topic) && (changed == "" || deck || changed == own),
			b.When != "" && b.Source != nil && slices.Contains(topicsOf(b, deck), topic) && readsProject(b, deck, own, changed),
			changesWhen(b.Blocks, record, deck, own, changed, topic):
			return true
		}
		for _, t := range b.Tabs {
			if changesWhen(t.Blocks, record, deck, own, changed, topic) {
				return true
			}
		}
	}
	return false
}

// screenTopics are the topics after which a client reads a screen again: those its blocks follow, the topic of the
// record of its page, and for a project screen projects, which draws it whole.
func screenTopics(bs []manifest.Block, record string, deck bool) []string {
	out := []string{}
	add := func(ts ...string) {
		for _, t := range ts {
			if !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	var walk func([]manifest.Block)
	walk = func(bs []manifest.Block) {
		for _, b := range bs {
			add(topicsOf(b, deck)...)
			walk(b.Blocks)
			for _, t := range b.Tabs {
				walk(t.Blocks)
			}
		}
	}
	walk(bs)
	if record != "" {
		add(readTopics(pageRead(deck, record))...)
	}
	if !deck {
		add("projects")
	}
	slices.Sort(out)
	return out
}
