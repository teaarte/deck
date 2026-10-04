package screen

import (
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
	case b.Source.Project != "":
		return b.Source.Project == project
	}
	if v, ok := b.Source.Input["project"]; ok {
		s, _ := v.(string)
		return s == "" || strings.HasPrefix(s, "=") || strings.HasPrefix(s, "$") || s == project
	}
	if c, ok := manifest.Reads(b.Source.From); ok && manifest.FieldTypes(c.Input)["project"] == "" {
		return true
	}
	return project == own
}

// topicsOf are the event topics after which the block is read again.
func topicsOf(b manifest.Block, deck bool) []string {
	switch {
	case b.Type == "board":
		return []string{"board"}
	case b.Type == "appearance", b.Type == "actions" && deck:
		return nil
	case b.Type == "document":
		return []string{"documents"}
	case b.Type == "form" && b.Source == nil:
		return []string{"collection"}
	}
	if b.Source == nil {
		if deck {
			return []string{"processes"}
		}
		return []string{"collection"}
	}
	if b.Source.From == "" || b.Source.From == "collection" {
		return []string{"collection"}
	}
	rc, _ := manifest.Reads(b.Source.From)
	return rc.Topics
}

// changesWhen reports whether an event of the topic in project changed may put a block of the screen of project
// own on it or take it off: its when reads its source or the record of the page. The screen is then resolved whole.
func changesWhen(bs []manifest.Block, record string, deck bool, own, changed, topic string) bool {
	page := "collection"
	switch {
	case deck:
		page = "processes"
	case record == manifest.DocumentRecord:
		page = "documents"
	}
	for _, b := range bs {
		switch {
		case b.When != "" && record != "" && topic == page && (changed == "" || deck || changed == own),
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
