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
			registry.Opt("record", "record", "Record the document belongs to"), registry.Opt("card", "card", "Card the document belongs to"),
			registry.Opt("reason", "string", "Why it changed")),
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

	add("cards_list", "Board", "Returns the board of a project: lanes in order with their cards by rank, and recently deleted cards when asked.", everyone,
		registry.Params(projectOpt, registry.Opt("deleted", "boolean", "Also list deleted cards that can still be restored")),
		registry.Typed(a.cardsList))
	add("card_get", "Get card", "Returns one card with the cards it waits for.", everyone,
		registry.Params(registry.P("card", "card", "Card id")), registry.Typed(a.cardGet))
	add("card_create", "Create card", "Creates a card at the end of a lane; without a lane it lands in the inbox lane.", everyone,
		registry.Params(projectOpt, registry.P("title", "string", "Card title"), registry.Opt("text", "text", "Card text, markdown"),
			registry.Opt("lane", "string", "Lane name"), registry.Opt("record", "record", "Record the card works on"),
			registry.Opt("waits", "card[]", "Cards this one waits for")),
		registry.Typed(a.cardCreate))
	add("card_update", "Edit card", "Changes the title, text, record or the cards a card waits for; omitted fields stay.", everyone,
		registry.Params(registry.P("card", "card", "Card id"), registry.Opt("title", "string", "New title"),
			registry.Opt("text", "text", "New text, markdown"), registry.Opt("record", "record", "Record the card works on; 0 unlinks it"),
			registry.Opt("waits", "card[]", "All cards this one waits for; an empty list clears them")),
		registry.Typed(a.cardUpdate))
	add("card_move", "Move card", "Moves a card to a lane, before another card or to the end; entering a lane starts its commands.", everyone,
		registry.Params(registry.P("card", "card", "Card id"), registry.P("lane", "string", "Lane name"),
			registry.Opt("before", "card", "Place before this card of the lane; the end when omitted")),
		registry.Typed(a.cardMove))
	add("card_delete", "Delete card", "Deletes a card; it can be restored for a day.", everyone,
		registry.Params(registry.P("card", "card", "Card id")), registry.Typed(a.cardDelete))
	add("card_restore", "Restore card", "Brings a deleted card back to its lane, or to the inbox lane when that lane is gone.", everyone,
		registry.Params(registry.P("card", "card", "Card id")), registry.Typed(a.cardRestore))

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

	add("process_stop", "Stop process", "Stops a process; a running agent is killed and what it did so far stays.", ownerSide,
		registry.Params(registry.P("process", "process", "Process id")), registry.Typed(a.processStop))
	add("process_rework", "Send back for rework", "Sends a finished agent process back to the same session with a remark; changed artifacts get new revisions.", ownerSide,
		registry.Params(registry.P("process", "process", "Process id"), registry.P("remark", "text", "What to change")),
		registry.Typed(a.processRework))
	add("process_log", "Process log", "Returns the last events of a process that is still open.", everyone,
		registry.Params(registry.P("process", "process", "Process id"), registry.Opt("limit", "integer", "At most this many events")),
		registry.Typed(a.processLog))

	add("run_later", "Run later", "Runs a project command once at the given time; a run missed while deck was down runs after it starts.", everyone,
		registry.Params(registry.P("command", "string", "Command name, project.command"), registry.P("at", "datetime", "When to run"),
			registry.Opt("params", "text", "Command input as a JSON object")),
		registry.Typed(a.runLater))
	add("run_later_cancel", "Cancel run later", "Cancels a run scheduled with run later.", everyone,
		registry.Params(registry.P("trigger", "integer", "Trigger id")), registry.Typed(a.runLaterCancel))
	add("triggers_list", "Schedule", "Lists triggers with their next run, pause and the state of their last run.", everyone,
		registry.Params(projectOpt), registry.Typed(a.triggersList))
	add("trigger_pause", "Pause trigger", "Pauses or resumes one trigger; a resumed schedule does not catch up its pause.", ownerSide,
		registry.Params(registry.P("trigger", "integer", "Trigger id"), registry.P("paused", "boolean", "Pause when true, resume when false")),
		registry.Typed(a.triggerPause))
	add("project_pause", "Pause project", "Pauses or resumes every trigger of a project, including triggers added later.", ownerSide,
		registry.Params(registry.P("project", "project", "Project name"), registry.P("paused", "boolean", "Pause when true, resume when false")),
		registry.Typed(a.projectPause))

	add("asks_list", "Waiting for you", "Lists open questions and confirmations for the owner.", ownerSide,
		registry.Params(), registry.Typed(a.asksList))
	add("health_list", "Health", "Lists the health of providers and other dependencies with the last error.", ownerSide,
		registry.Params(), registry.Typed(a.healthList))
	add("ask_answer", "Answer", "Answers a question or confirmation: text, allow, deny or deny:<reason>; a login ask: retry or new_session.", ownerOnly,
		registry.Params(registry.P("ask", "ask", "Ask id"), registry.P("answer", "text", "The answer")),
		registry.Typed(a.askAnswer))
}
