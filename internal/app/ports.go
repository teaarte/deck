package app

import (
	"deck2/internal/app/chat"
	"deck2/internal/app/documents"
	"deck2/internal/app/folders"
	"deck2/internal/app/signins"
)

// The services of the ports of the runner by name, so one port embeds several.
type (
	docsOf    = documents.Service
	chatOf    = chat.Service
	foldersOf = folders.Service
	signinsOf = signins.Service
)

// brief is what deck gives a turn (process.Brief): the documents and skills of a project, the memory and language of
// its chat.
type brief struct {
	*docsOf
	*chatOf
}

// places are where the work of a project runs (process.Places): its folders, its MCP servers and the programs of its
// scripts.
type places struct {
	*foldersOf
	*signinsOf
	*docsOf
}
