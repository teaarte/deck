package events

// The topics of events: a write of a table publishes the topic of its table, the rest deck publishes by hand.
const (
	Assignments = "assignments"
	Asks        = "asks"
	Calls       = "calls"
	Chat        = "chat"
	Collection  = "collection"
	Devices     = "devices"
	Documents   = "documents"
	Feed        = "feed"
	Files       = "files"
	Folders     = "folders"
	Health      = "health"
	Models      = "models"
	Processes   = "processes"
	Projects    = "projects"
	Settings    = "settings"
	Tools       = "tools"
	Triggers    = "triggers"
	Usage       = "usage"
	// Log, Secrets and Snapshots are of what deck keeps in streams and files, not in tables.
	Log       = "log"
	Secrets   = "secrets"
	Snapshots = "snapshots"
	// Outbox is the topic of a row put in the outbox: deck's own, never published to a client.
	Outbox = "outbox"
)

// Topics are the topics deck publishes to clients, in sorted order; Outbox is deck's own.
var Topics = []string{Asks, Assignments, Calls, Chat, Collection, Devices, Documents, Feed, Files, Folders, Health, Log,
	Models, Processes, Projects, Secrets, Settings, Snapshots, Tools, Triggers, Usage}
