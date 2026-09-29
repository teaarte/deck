package app

import (
	"context"
	"encoding/json"

	"deck2/internal/app/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
)

var (
	everyone   = []string{registry.Owner, registry.Master, registry.Agent}
	ownerSide  = []string{registry.Owner, registry.Master}
	ownerOnly  = []string{registry.Owner}
	projectOpt = registry.Opt("project", "project", "Project name; defaults to the caller's own project")
	ifRevision = registry.Opt("if_revision", "integer", "Only while the current revision is still this one")
)

func (a *App) addBuiltins() {
	r := a.Registry
	add := func(name, title, desc string, callers []string, in json.RawMessage, h registry.Handler) {
		r.Add(&registry.Command{Name: registry.Builtin + "." + name, Title: title, Description: desc,
			Class: registry.Internal, Callers: callers, Input: in, Handler: h})
	}

	add("projects_list", "List projects", "Lists projects with their description (the first line of the md prompt) and screens.", ownerSide,
		registry.Params(), registry.Typed(a.projectsList))
	add("project_create", "Create project", "Creates a project from its name, an md prompt and a manifest; without a manifest it has only the base board.", ownerSide,
		registry.Params(registry.P("name", "string", "Project name: lowercase letters, digits and '-'"),
			registry.Opt("manifest", "text", "Manifest JSON; a project with only the base board when omitted"), registry.Opt("prompt", "text", "Project md prompt")),
		registry.Typed(a.projectCreate))
	add("project_enable", "Enable project", "Turns a draft on: its triggers start running and the rights its manifest asks for count as allowed.", ownerOnly,
		registry.Params(registry.P("project", "project", "Project name"),
			registry.P("revision", "integer", "The manifest revision whose rights the owner looked at")), registry.Typed(a.projectEnable))
	add("project_autonomy", "Project autonomy", "Sets whether a project suggests, so the owner says yes to its reversible changes, or acts on its own and reports; a new manifest revision.", ownerOnly,
		registry.Params(registry.P("project", "project", "Project name"),
			registry.Param{Name: "autonomy", Type: "enum", Desc: "suggest or auto", Enum: []string{"suggest", "auto"}}),
		registry.Typed(a.projectAutonomy))
	add("command_try", "Trial run", "Starts a trial run of a project command: calls that are not internal are declined, nothing leaves deck.", ownerSide,
		registry.Params(registry.P("command", "string", "Command name, project.command"), registry.Opt("input", "text", "Command input as a JSON object")),
		registry.Typed(a.commandTry))
	add("project_import", "Import project", "Creates a draft project from a module folder: manifest.json, prompt.md and the other files by their document names.", ownerSide,
		registry.Params(registry.P("folder", "string", "Absolute path of the folder"), registry.Opt("name", "string", "Project name; the folder name when omitted")),
		registry.Typed(a.projectImport))
	add("folder_add", "Add folder", "Gives a project a folder on disk that its agent commands with folder work in; only the owner, since it lets agents write there.", ownerOnly,
		registry.Params(registry.P("project", "project", "Project name"), registry.P("path", "string", "Absolute path of the folder"),
			registry.Opt("name", "string", "Folder name in the project; the folder's own name when omitted")),
		registry.Typed(a.folderAdd))
	add("folder_remove", "Remove folder", "Takes a folder from a project; the folder on disk stays as it is.", ownerOnly,
		registry.Params(registry.P("project", "project", "Project name"), registry.P("name", "string", "Folder name in the project")),
		registry.Typed(a.folderRemove))
	add("unattended_list", "Done without you", "Lists changes made without the owner, newest first, with whether they can be undone.", ownerSide,
		registry.Params(projectOpt, registry.Opt("limit", "integer", "At most this many")), registry.Typed(a.unattendedList))
	add("unattended_undo", "Undo", "Returns what a change made without the owner touched to how it was before.", ownerOnly,
		registry.Params(registry.P("entry", "integer", "Entry id from done without you")), registry.Typed(a.unattendedUndo))
	add("token_create", "Create automation token", "Creates a token for a terminal agent, Shortcuts or a script; it acts as the master and may call only the listed commands.", ownerOnly,
		registry.Params(registry.P("name", "string", "What the token is for"),
			registry.P("commands", "string[]", "Command names it may call; project.* or deck.* allow every command under the prefix")),
		registry.Typed(a.tokenCreate))
	add("tokens_list", "Automation tokens", "Lists automation tokens with their commands; revoked ones too.", ownerOnly,
		registry.Params(), registry.Typed(a.tokensList))
	r.Add(&registry.Command{Name: registry.Builtin + ".token_revoke", Title: "Revoke token",
		Description: "Revokes an automation token; calls with it are refused from then on.", Class: registry.External,
		Callers: ownerOnly, Input: registry.Params(registry.P("token", "integer", "Token id")), Handler: registry.Typed(a.tokenRevoke)})

	add("manifest_get", "Get manifest", "Returns the current manifest of a project.", everyone,
		registry.Params(projectOpt), registry.Typed(a.manifestGet))
	add("manifest_format", "Manifest format", "Returns the JSON Schema of project manifests.", everyone,
		registry.Params(), registry.Typed(func(context.Context, registry.Actor, struct{}) (any, error) {
			return json.RawMessage(manifest.Format), nil
		}))
	add("manifest_save", "Save manifest", "Validates a manifest and stores it as a new revision.", ownerSide,
		registry.Params(registry.P("project", "project", "Project name"), registry.P("manifest", "text", "Manifest JSON"),
			registry.Opt("reason", "string", "Why it changed"), ifRevision),
		registry.Typed(a.manifestSave))

	add("documents_list", "List documents", "Lists documents of a project, or workspace documents when no project is given.", everyone,
		registry.Params(projectOpt, registry.Param{Name: "kind", Type: "enum", Desc: "Only this kind", Enum: docs.Kinds, Optional: true}),
		registry.Typed(a.documentsList))
	add("document_get", "Get document", "Returns a document by id, or by project and name; the current revision unless one is given.", everyone,
		registry.Params(registry.Opt("document", "document", "Document id"), projectOpt,
			registry.Opt("name", "string", "Document name (path)"), registry.Opt("revision", "integer", "Revision number")),
		registry.Typed(a.documentGet))
	add("document_save", "Save document", "Writes a new revision of a document, creating it on first save.", everyone,
		registry.Params(registry.P("name", "string", "Document name (path), e.g. notes/criteria.md"),
			registry.Param{Name: "kind", Type: "enum", Desc: "Document kind", Enum: docs.Kinds},
			registry.P("text", "text", "Full new text"), projectOpt,
			registry.Opt("record", "record", "Record the document belongs to"), registry.Opt("card", "card", "Card the document belongs to"),
			registry.Opt("reason", "string", "Why it changed")),
		registry.Typed(a.documentSave))
	add("document_revisions", "Document history", "Lists revisions of a document: who, when and why.", everyone,
		registry.Params(registry.P("document", "document", "Document id")), registry.Typed(a.documentRevisions))
	add("document_revert", "Revert document", "Makes an earlier revision current again as a new revision.", ownerSide,
		registry.Params(registry.P("document", "document", "Document id"), registry.P("revision", "integer", "Revision to return to"),
			registry.Opt("reason", "string", "Why"), ifRevision),
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

	add("file_upload", "Upload file", "Uploads a file for a project from base64 text and returns its id for a command input.",
		[]string{registry.Owner, registry.Master}, registry.Params(registry.P("project", "project", "Project name"),
			registry.P("name", "string", "File name with its extension"), registry.P("content", "text", "File content in base64")),
		registry.Typed(a.fileUpload))
	add("record_get", "Get record", "Returns one record by its id.", everyone,
		registry.Params(registry.P("record", "record", "Record id")), registry.Typed(a.recordGet))
	add("record_delete", "Delete record", "Deletes a record with its documents and files for good; outside a draft only the owner may.",
		[]string{registry.Owner, registry.Master}, registry.Params(registry.P("record", "record", "Record id")), registry.Typed(a.recordDelete))

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

	r.Add(&registry.Command{Name: registry.Builtin + ".project_delete", Title: "Delete project",
		Description: "Deletes a project after a snapshot of deck: its records, documents, files, triggers, cards, processes and settings; folders on disk stay.",
		Class:       registry.External, Callers: ownerOnly,
		Input:   registry.Params(registry.P("project", "project", "Project name")),
		Handler: registry.Typed(a.projectDelete)})
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
	add("trigger_run", "Run now", "Starts the command of a schedule trigger now with its params; its schedule stays, and a run still going is returned.", ownerSide,
		registry.Params(registry.P("trigger", "integer", "Trigger id")), registry.Typed(a.triggerRun))
	add("project_pause", "Pause project", "Pauses or resumes every trigger of a project, including triggers added later.", ownerSide,
		registry.Params(registry.P("project", "project", "Project name"), registry.P("paused", "boolean", "Pause when true, resume when false")),
		registry.Typed(a.projectPause))

	add("files_list", "List files", "Lists the files of a record, a card or a process.", everyone,
		registry.Params(registry.Opt("record", "record", "Record"), registry.Opt("card", "card", "Card"),
			registry.Opt("process", "process", "Process")), registry.Typed(a.filesList))
	add("calls_list", "Journal of calls", "Lists the calls of the gateway, newest first: tool, who, project, process, class, outcome, external id and error; params only of external and waiting calls.", ownerOnly,
		registry.Params(projectOpt, registry.Opt("outcome", "string", "Only this outcome: done, rejected, waiting, expired, unknown, started"),
			registry.Opt("limit", "integer", "At most this many, 200 by default")), registry.Typed(a.callsList))
	add("snapshot_create", "Take snapshot", "Writes a snapshot of the base, files and project data folders to backups; the newest seven stay.", ownerOnly,
		registry.Params(), registry.Typed(a.snapshotCreate))

	add("settings_set", "Change settings", "Sets how long a confirmation waits for the owner and how many agent processes run at once; an omitted one stays.", ownerOnly,
		registry.Params(registry.Opt("confirm_hours", "integer", "Hours a confirmation waits, 1 to 720"),
			registry.Opt("agent_limit", "integer", "Agent processes at once, 1 to 20; more wait for a slot")),
		registry.Typed(a.settingsSet))
	add("secret_set", "Set secret", "Stores a secret for tool headers as a file only deck reads; agents and scripts never see it.", ownerOnly,
		registry.Params(registry.P("name", "string", "Secret name, as in $secret.<name>"), registry.P("value", "secret", "Secret value; it is never shown again")),
		registry.Typed(a.secretSet))
	add("secrets_list", "Secrets", "Lists the names of stored secrets, never their values.", ownerOnly,
		registry.Params(), registry.Typed(a.secretsList))
	add("browser_login", "Sign in to a site", "Opens a window of deck's browser at a page so the owner signs in once with deck's own profile.", ownerOnly,
		registry.Params(registry.P("url", "string", "Page to open, such as the site's sign-in page")), registry.Typed(a.browserLogin))
	add("browser_login_done", "Signed in", "Closes the sign-in window of deck's browser; processes waiting for this site go on.", ownerOnly,
		registry.Params(registry.P("url", "string", "A page of the site the owner signed in to")), registry.Typed(a.browserLoginDone))
	add("browser_needs_login", "Site needs a sign-in", "Tells the owner that the session of a site ran out; the process waits until the owner signs in again.",
		[]string{registry.Agent}, registry.Params(registry.P("url", "string", "The page that asked for a sign-in"),
			registry.P("why", "string", "What the page showed")), registry.Typed(a.browserNeedsLogin))
	add("oauth_start", "Sign in to a service", "Starts the owner's oauth2 sign-in for a tool in a window of deck's browser; it must finish within ten minutes.", ownerOnly,
		registry.Params(registry.P("tool", "string", "The tool, as project.tool")), registry.Typed(a.oauthStart))
	add("providers_list", "Providers", "Lists model providers with what they can do and their health; with a command, which fit it and which it runs on.", ownerSide,
		registry.Params(registry.Opt("command", "string", "Agent command, project.command")), registry.Typed(a.providersList))
	add("provider_set", "Choose provider", "Sets the provider an agent command runs on from its next start; empty goes back to the default.", ownerOnly,
		registry.Params(registry.P("command", "string", "Agent command, project.command"),
			registry.Opt("provider", "string", "Provider name; empty for the default")), registry.Typed(a.providerSet))
	add("provider_add", "Add provider", "Adds or changes an OpenAI-compatible provider of this workspace, such as Ollama, OpenRouter or DeepSeek, run by deck or by an ACP agent.", ownerOnly,
		registry.Params(registry.P("name", "string", "Provider name: lowercase letters, digits and '-'"),
			registry.P("url", "string", "Base URL of the OpenAI-compatible API, such as http://localhost:11434/v1"),
			registry.P("model", "string", "Model name"), registry.Opt("key_secret", "string", "Name of the secret with the API key"),
			registry.Opt("no_cloud", "boolean", "The model runs on the owner's own hardware"),
			registry.Opt("agent", "string", "An ACP agent that runs the model inside the sandbox of deck with a shell and files: opencode or dsh; without it deck calls the model with its own tool loop")),
		registry.Typed(a.providerAdd))
	add("provider_remove", "Remove provider", "Removes an OpenAI-compatible provider; commands set to it must get another.", ownerOnly,
		registry.Params(registry.P("name", "string", "Provider name")), registry.Typed(a.providerRemove))
	add("provider_check", "Check provider", "Runs the self-check of a provider's adapter again; its health shows the outcome.", ownerOnly,
		registry.Params(registry.P("provider", "string", "Provider name")), registry.Typed(a.providerCheck))
	add("llm_calls_list", "Model calls", "Lists model calls with their provider, model, tokens and duration, newest first.", everyone,
		registry.Params(projectOpt, registry.Opt("process", "process", "Only calls of this process"),
			registry.Opt("limit", "integer", "At most this many")), registry.Typed(a.llmCallsList))
	add("asks_list", "Waiting for you", "Lists open questions and confirmations for the owner.", ownerSide,
		registry.Params(), registry.Typed(a.asksList))
	add("health_list", "Health", "Lists the health of providers and other dependencies with the last error.", ownerSide,
		registry.Params(), registry.Typed(a.healthList))
	add("ask_answer", "Answer", "Answers what waits for the owner: a question with text; a confirmation with allow, allow_always, deny or deny:<reason>; a login with retry or new_session; an unknown outcome with retry or done; a stopped pause with continue. Deny on an expired confirmation clears it.", ownerOnly,
		registry.Params(registry.P("ask", "ask", "Ask id"), registry.P("answer", "text", "The answer"),
			registry.Opt("reason", "text", "Why not, sent with deny to the agent")),
		registry.Typed(a.askAnswer))
	add("menu", "Menu", "Lists the screens to navigate: deck's own, then each project's menu.", ownerSide,
		registry.Params(), registry.Typed(a.menu))
	add("screen", "Screen", "Resolves a stored screen with its data into A2UI messages; with a topic, only the data of the blocks it changes.", ownerSide,
		registry.Params(registry.Opt("project", "project", "Project name; deck's own screens without one"),
			registry.P("screen", "string", "Screen name"), registry.Opt("record", "integer", "Record id of a record page"),
			registry.Opt("topic", "string", "Event topic that changed"),
			registry.Opt("filter", "text", "Values the owner picked in block filters, as JSON: block id to field to value")),
		registry.Typed(a.screenGet))
	add("notify", "Notify the owner", "Writes a line to the feed of the cockpit, with a screen of the project to open and buttons; the caller decides it is worth it.",
		everyone, registry.Params(projectOpt, registry.P("text", "text", "What happened, in a sentence"),
			registry.Opt("screen", "string", "Screen of the project the line opens"), registry.Opt("record", "record", "Record the screen shows"),
			registry.Opt("actions", "text", "Buttons as JSON: [{label, command, params}] with commands of the project")),
		registry.Typed(a.notify))
}
