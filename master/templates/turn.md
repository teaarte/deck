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

{{end}}{{if .Summarized}}The summary of this topic is document {{.Summary}} of project master, at revision {{.Revision}}, with {{.After}} messages after it; change it with old and new and if_revision {{.Revision}}; read it with document_get only for lines you do not have.{{else}}This topic has no summary yet; deck asks for one when it grows, as document {{.Summary}} of project master.{{end}}{{if .Grown}} The topic has {{.Grown}} messages after its summary: update it by the topic-summary skill.{{end}}
{{with .Projects}}{{.}}
{{end}}{{with .Open}}{{.}}
{{end}}
{{if .Language}}deck gives this topic the language {{.Language}}. Is the owner's latest message above written in {{.Language}}? If not, your first call is `deck.chat_topic_lang_set` with the code of its language (such as ru or en), and you reply in it: the works, requests and words of this topic follow that mark, not your reply.{{else}}deck knows no language of this topic yet: your first call is `deck.chat_topic_lang_set` with the code of the language of the owner's latest message above (such as ru or en), and you reply in it.{{end}}
