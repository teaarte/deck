{{with .Notes}}deck: since your last turn:
{{.}}

{{end}}{{if .MemoryChanged}}deck: the memory changed since this session last got it; here it is now.
{{.Memory}}
{{end}}{{if .Retold}}deck: much happened in this topic since your last turn.
{{.Retold}}
{{else if .Missed}}deck: these messages of the topic came after your last turn; another provider may have answered them.
{{.Missed}}

{{end}}{{if gt (len .Pending) 1}}The owner's {{len .Pending}} new messages in chat topic {{.Topic}}, oldest first:
{{else}}The owner's new message in chat topic {{.Topic}}:
{{end}}{{range .Pending}}{{if .Where}}(written on {{.Where}})
{{end}}{{.Text}}

{{end}}{{with .Card}}This topic is about card {{.ID}} of project {{.Project}}, titled {{.Title}}.
{{end}}{{if .Summarized}}The summary of this topic is document {{.Summary}} of project master.{{else}}This topic has no summary yet; deck asks for one when it grows, as document {{.Summary}} of project master.{{end}}{{if .Grown}} The topic has {{.Grown}} messages after its summary: update it by the topic-summary skill.{{end}}