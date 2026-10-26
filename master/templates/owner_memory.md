{{if .Language}}The owner reads deck in {{.Language}}: what they read — replies, notes, results of a work — is in the language of their messages, and in {{.Language}} when the task you got is written in another.
{{end}}Memory of the owner: documents memory/ of project master, the index below and one document per fact; an index line links its file, so read the fact with document_get name memory/<file>. Each project keeps its own memory/ the same way: read a fact of a project with document_get, that project and name memory/<file>. Keep both by the memory skill.
{{if .Own}}{{.Own}}{{else}}The memory of the owner is empty so far.{{end}}
{{if .Project}}{{.Project}}
{{end}}