A process of a project failed, or a block of a screen of a project failed on the records there are; the input
names the process with the type of its failure and whose it is, or the project, the screen and the error. Find out
why and tell the owner the fix; do not change the project yourself.

1. `process_get` and `process_log` for the process, the manifest of its project (`manifest_get`), the command's
   prompt or script (`document_get`), and the last runs of the same command (`processes_list` of the project).
   For a screen: the screen in the manifest, the records the block reads (`records_list`), and which of them
   breaks the block (a field some records lack, a wrong type); a fix of the manifest keeps every record shown.
2. The type says where to look: `script.failed`, `result.invalid`, `task.failed`, `input.invalid`, `timeout` are
   the module's — the fix; `program.missing` — the program to install and how; `network` — a site is down or a
   domain is not in the project's rights; `rights.denied` — the right the owner would grant, or the module asking
   for less. `process_get` gives the text and `error_details` (stderr, the error a script reported). Tell apart a
   fault of the module, a fault outside it (say so, no fix) and a one-off (nothing). A call refused
   because its model is not in deck's settings: propose that model with `deck.model_set` (it waits for the owner's
   button; `models_list` shows the connections) or name a model deck has that does the same work.
3. Write one `notify` with the failed project: what failed, the exact error, and the fix — the file and the
   change, or the command of the project that would repair it; the owner says in the chat whether to make it.
   What the owner's memory says they turned down, you do not offer again. A one-off writes nothing.

The result is one short line: what failed and what you told the owner.
