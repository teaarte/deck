A process of a project failed, or a block of a screen of a project failed on the records there are; the input
names the process, or the project, the screen and the error. Find out why and suggest a fix; do not change the
project yourself.

1. `process_get` and `process_log` for the process, the manifest of its project (`manifest_get`), the command's
   prompt or script (`document_get`), and the last runs of the same command (`processes_list` of the project).
   For a screen: the screen in the manifest, the records the block reads (`records_list`), and which of them
   breaks the block (a field some records lack, a wrong type); a fix of the manifest keeps every record shown.
2. Tell apart: a fault of the module (the script, the prompt, the schema, a wrong tool) — suggest the fix; a
   fault outside it (a site is down, a login ran out, a limit) — say so, no fix; a one-off — nothing.
3. A fix is one `suggest`: a card in the failed project with what to change and why (the exact error, the file
   and the change), or a call of a command of the project that would repair it. Read `suggestions_rejected` first,
   so a fix the owner turned down does not come back.

The result is one short line: what failed and what you suggested.
