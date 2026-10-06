A process of a project failed, or a block of a screen of a project failed on the records there are; the input
names the process, or the project, the screen and the error. Find out why and tell the owner the fix; do not change
the project yourself.

1. `process_get` and `process_log` for the process, the manifest of its project (`manifest_get`), the command's
   prompt or script (`document_get`), and the last runs of the same command (`processes_list` of the project).
   For a screen: the screen in the manifest, the records the block reads (`records_list`), and which of them
   breaks the block (a field some records lack, a wrong type); a fix of the manifest keeps every record shown.
2. Tell apart: a fault of the module (the script, the prompt, the schema, a wrong tool) — the fix; a fault
   outside it (a site is down, a login ran out, a limit) — say so, no fix; a one-off — nothing.
3. Write one `notify` with the failed project: what failed, the exact error, and the fix — the file and the
   change, or the command of the project that would repair it; the owner says in the chat whether to make it.
   What the owner's memory says they turned down, you do not offer again. A one-off writes nothing.

The result is one short line: what failed and what you told the owner.
