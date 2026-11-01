---
name: incident-recovery
description: Mends an incident deck noticed around a process of a project — a result handed in that a stop or a hang kept from going on, a step after that did not start — from the cheapest check up, and tells the owner in one line of the feed. Use in master.on_incident, and before you stop a process yourself.
---

# Mending an incident

Deck notices and tells; it mends nothing. You mend what is inside deck and can be undone, and offer the rest. Work
from the cheapest check up and stop at the first that settles it.

## 1. Facts first, no reading of logs

`process_get` of the process gives its state, its result, whether it handed one in (`submitted_at`) and its record.
`manifest_get` of its project gives the steps after its command (`triggers` with `after`), and `processes_list` of
the project with the record gives what ran for that record since.

- **result_running** — the process handed in its result and its turn goes on. The result is kept and the steps
  after it already started. Stop the process (`process_stop`): it ends done with its result. Nothing else.
- **result_not_done** — the process handed in a result, then closed other than done, such as when deck could not keep
  a file its result names. When the files are there to keep and no later run of the same command for the record is
  done, run the command again for the record, or the step after it the way its trigger would: the command of the
  trigger with the record (and `process` when the step takes it).
- **after_not_started** — a step after a done process did not start; the text says why. When the cause is gone
  (a paused project resumed, a model is back), start the step as above; when it is not, offer the fix.
- A duplicate — two runs of a command for the same record, one done: stop the one still running unless the owner's
  call started it.

## 2. The log, only when the facts do not settle it

`process_log_get` with `only: messages` from the end: the agent's last words and the result it meant to hand in.
A result in the log that no process kept goes through the same command or the step after it, as above.

## 3. Rules

- Never stop a process that handed in its result without reading `process_get` first: its result must be there.
- What is inside deck and can be undone — a record, a step after, a stop of a duplicate — do yourself. A command
  of class `external` or `paid`, a new model, a change of a module — offer only.
- Write one `owner_notify` with the project of the process: what happened, what you did, and what the owner can
  undo or say to have the rest done. Nothing done and nothing to offer — write nothing.
