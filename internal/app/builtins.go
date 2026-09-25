package app

import (
	"context"
	"encoding/json"

	"deck2/internal/registry"
	"deck2/schemas"
)

var (
	everyone   = []string{registry.Owner, registry.Master, registry.Agent}
	ownerSide  = []string{registry.Owner, registry.Master}
	ownerOnly  = []string{registry.Owner}
	projectOpt = registry.Opt("project", "project", "Project name; defaults to the caller's own project")
)

func (a *App) addBuiltins() {
	r := a.Registry
	add := func(name, title, desc string, callers []string, in json.RawMessage, h registry.Handler) {
		r.Add(&registry.Command{Name: registry.Builtin + "." + name, Title: title, Description: desc,
			Class: registry.Internal, Callers: callers, Input: in, Handler: h})
	}

	add("projects_list", "List projects", "Lists projects with their description (the first line of the md prompt) and screens.", ownerSide,
		registry.Params(), registry.Typed(a.projectsList))
	add("project_create", "Create project", "Creates a project from a manifest and an optional md prompt.", ownerSide,
		registry.Params(registry.P("name", "string", "Project name: lowercase letters, digits and '-'"),
			registry.P("manifest", "text", "Manifest JSON"), registry.Opt("prompt", "text", "Project md prompt")),
		registry.Typed(a.projectCreate))
	add("manifest_get", "Get manifest", "Returns the current manifest of a project.", everyone,
		registry.Params(projectOpt), registry.Typed(a.manifestGet))
	add("manifest_format", "Manifest format", "Returns the JSON Schema of project manifests.", everyone,
		registry.Params(), registry.Typed(func(context.Context, registry.Actor, struct{}) (any, error) {
			return json.RawMessage(schemas.Manifest), nil
		}))
	add("manifest_save", "Save manifest", "Validates a manifest and stores it as a new revision.", ownerSide,
		registry.Params(registry.P("project", "project", "Project name"), registry.P("manifest", "text", "Manifest JSON"),
			registry.Opt("reason", "string", "Why it changed")),
		registry.Typed(a.manifestSave))

	add("documents_list", "List documents", "Lists documents of a project, or workspace documents when no project is given.", everyone,
		registry.Params(projectOpt, registry.Param{Name: "kind", Type: "enum", Desc: "Only this kind", Enum: DocumentKinds, Optional: true}),
		registry.Typed(a.documentsList))
	add("document_get", "Get document", "Returns a document by id, or by project and name; the current revision unless one is given.", everyone,
		registry.Params(registry.Opt("document", "document", "Document id"), projectOpt,
			registry.Opt("name", "string", "Document name (path)"), registry.Opt("revision", "integer", "Revision number")),
		registry.Typed(a.documentGet))
	add("document_save", "Save document", "Writes a new revision of a document, creating it on first save.", everyone,
		registry.Params(registry.P("name", "string", "Document name (path), e.g. notes/criteria.md"),
			registry.Param{Name: "kind", Type: "enum", Desc: "Document kind", Enum: DocumentKinds},
			registry.P("text", "text", "Full new text"), projectOpt,
			registry.Opt("record", "record", "Record the document belongs to"), registry.Opt("reason", "string", "Why it changed")),
		registry.Typed(a.documentSave))
	add("document_revisions", "Document history", "Lists revisions of a document: who, when and why.", everyone,
		registry.Params(registry.P("document", "document", "Document id")), registry.Typed(a.documentRevisions))
	add("document_revert", "Revert document", "Makes an earlier revision current again as a new revision.", ownerSide,
		registry.Params(registry.P("document", "document", "Document id"), registry.P("revision", "integer", "Revision to return to"),
			registry.Opt("reason", "string", "Why")),
		registry.Typed(a.documentRevert))

	add("records_list", "List records", "Lists records of a collection, newest last.", everyone,
		registry.Params(projectOpt, registry.P("collection", "string", "Collection name"), registry.Opt("limit", "integer", "At most this many")),
		registry.Typed(a.recordsList))
	add("records_upsert", "Write records", "Writes records into a collection; records with an existing key are updated.", everyone,
		registry.Params(projectOpt, registry.P("collection", "string", "Collection name"),
			registry.P("records", "text", "Records as JSONL: one JSON object per line")),
		registry.Typed(a.recordsUpsert))
	add("record_update", "Edit record", "Replaces the data of one record; key fields cannot change.", everyone,
		registry.Params(registry.P("record", "record", "Record id"), registry.P("data", "text", "New record JSON")),
		registry.Typed(a.recordUpdate))

	r.Add(&registry.Command{Name: registry.Builtin + ".collection_delete", Title: "Delete collection records",
		Description: "Deletes every record of a collection, so the collection can be removed from the manifest.",
		Class:       registry.External, Callers: ownerOnly,
		Input:   registry.Params(registry.P("project", "project", "Project name"), registry.P("collection", "string", "Collection name")),
		Handler: registry.Typed(a.collectionDelete)})

	add("processes_list", "List processes", "Lists recent processes, newest first.", everyone,
		registry.Params(projectOpt, registry.Opt("state", "string", "Only this state"), registry.Opt("limit", "integer", "At most this many")),
		registry.Typed(a.processesList))
	add("process_get", "Process status", "Returns the state and result of a process.", everyone,
		registry.Params(registry.P("process", "process", "Process id")), registry.Typed(a.processGet))

	add("asks_list", "Waiting for you", "Lists open questions and confirmations for the owner.", ownerSide,
		registry.Params(), registry.Typed(a.asksList))
	add("ask_answer", "Answer", "Answers a question or confirmation: text, allow, deny or deny:<reason>.", ownerOnly,
		registry.Params(registry.P("ask", "ask", "Ask id"), registry.P("answer", "text", "The answer")),
		registry.Typed(a.askAnswer))
}
