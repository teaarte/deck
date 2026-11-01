package process

import "time"

// Incidents are what deck notices went wrong around a process and hands to the master; deck mends none itself.
const (
	// IncidentRunning is a process that handed in its result and still runs SubmittedLinger later.
	IncidentRunning = "result_running"
	// IncidentNotDone is a process that handed in a result and closed other than done.
	IncidentNotDone = "result_not_done"
	// IncidentAfter is a step after a process that did not start.
	IncidentAfter = "after_not_started"
)

// SubmittedLinger is how long a process may run after it handed in its result before it is an incident.
const SubmittedLinger = 10 * time.Minute
