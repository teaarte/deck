package harness

import (
	"encoding/json"
)

// DeckCallScript calls the deck command of its input's method with the JSON of form and prints the status and body.
const DeckCallScript = `import json, os, sys, urllib.request, urllib.error
inp = json.load(sys.stdin)
req = urllib.request.Request(os.environ["DECK_URL"] + "/commands/" + inp["method"], data=inp["form"].encode(),
    headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
try:
    r = urllib.request.urlopen(req, timeout=30)
    status, body = r.status, r.read().decode()
except urllib.error.HTTPError as e:
    status, body = e.code, e.read().decode()
print(json.dumps({"status": status, "body": body}))
`

type Called struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// ScriptCall runs the script command main of project with a call of method and returns what deck answered it.
func (e *Env) ScriptCall(project, method string, form map[string]any) Called {
	e.T.Helper()
	raw, _ := json.Marshal(form)
	p := e.WaitState(e.Start(project, "main", map[string]any{"method": method, "form": string(raw)}), "done", "error")
	var c Called
	if err := json.Unmarshal(p.Result, &c); err != nil {
		e.T.Fatalf("the script answers what deck said: %+v %v", p, err)
	}
	return c
}

// Assigned are the models of a role of a project as the owner reads them.
func (e *Env) Assigned(project, role string) []string {
	e.T.Helper()
	var m ModelsOut
	e.Must("deck.models_list", map[string]any{"project": project}, &m)
	for _, a := range m.Assignments {
		if a.Project == project && a.Command == "" && a.Role == role {
			return a.Models
		}
	}
	return nil
}

// AskState is the state of an ask as the store keeps it.
func (e *Env) AskState(id int64) string {
	var state string
	_ = e.App.Store.DB.QueryRow(`SELECT state FROM ask WHERE id = ?`, id).Scan(&state)
	return state
}
