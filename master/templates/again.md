{{if eq .Why "killed"}}deck: a signal from outside deck killed your turn mid-work. What you did so far stays: check it, do not redo it, and go on where you stopped.
{{if .Submit}}When you are done, hand in the whole final result with the result_submit tool of the deck MCP server, then end your turn.
{{end}}{{else if eq .Why "resume"}}
When you are done, hand in the whole final result with the result_submit tool of the deck MCP server, then end your turn.
{{else if .Submit}}You ended your turn without handing in the result with the result_submit tool: {{.Error}}
Do not redo the work. Call result_submit with only the JSON result.{{else}}Your final JSON did not match the result schema: {{.Error}}
Do not redo the work. Send only the corrected JSON.{{end}}
