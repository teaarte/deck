package screen

import (
	"slices"

	"deck2/internal/manifest"
)

// follows reports whether the block is read again after an event of the topic.
func follows(b manifest.Block, deck bool, topic string) bool {
	return slices.Contains(topicsOf(b, deck), topic)
}

// reads says whether a block reads a part of a project: a block of a deck screen reads every project.
func (r *renderer) reads(b manifest.Block, project string) bool {
	switch {
	case project == "" || r.deck():
		return true
	case b.Source != nil && b.Source.Project != "":
		return b.Source.Project == project
	}
	return project == r.req.Project.Name
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

// changesWhen reports whether an event of the topic may put a block on the screen or take it off: its when
// reads its source or the record of the page. The screen is then resolved whole.
func changesWhen(bs []manifest.Block, record string, deck bool, topic string) bool {
	page := "collection"
	switch {
	case deck:
		page = "processes"
	case record == manifest.DocumentRecord:
		page = "documents"
	}
	for _, b := range bs {
		switch {
		case b.When != "" && record != "" && topic == page,
			b.When != "" && b.Source != nil && slices.Contains(topicsOf(b, deck), topic),
			changesWhen(b.Blocks, record, deck, topic):
			return true
		}
		for _, t := range b.Tabs {
			if changesWhen(t.Blocks, record, deck, topic) {
				return true
			}
		}
	}
	return false
}
