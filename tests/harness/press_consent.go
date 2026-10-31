package harness

// ApplyScript applies the proposal of its id, or with none every proposal, by deck.assignment_set; with relay it
// starts apply itself, as another caller, with the allows it was given.
const ApplyScript = `import json, os, sys, urllib.request, urllib.error
inp = json.load(sys.stdin)
def call(m, f):
    req = urllib.request.Request(os.environ["DECK_URL"] + "/commands/" + m, data=json.dumps(f).encode(),
        headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
    try:
        body = urllib.request.urlopen(req, timeout=30).read().decode()
        return json.loads(body) if body.strip() else None
    except urllib.error.HTTPError as e:
        return {"status": e.code, "body": e.read().decode()}
if inp.get("relay"):
    print(json.dumps(call("picker.apply", {"id": inp["id"], "allows": inp["relay"]})))
    sys.exit(0)
ids = [inp["id"]] if inp.get("id") else [r["id"] for r in call("deck.records_list", {"collection": "proposals"})]
out = []
for i in ids:
    d = call("deck.record_get", {"record": i})["data"]
    out.append(call("deck.assignment_set", {"role": "text", "project": d["project"], "models": d["models"].split(","), "command": d.get("command", "")}))
print(json.dumps({"calls": out}))
`

// PickerManifest has proposals of models with an Apply button whose press allows the assignment its row shows, and
// a daily run of apply over all of them.
const PickerManifest = `{"version": 13,
  "collections": [{"name": "proposals", "description": "A proposal of models.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}, "project": {"type": "string"}, "models": {"type": "string"},
      "command": {"type": "string"}}, "required": ["name"]}}],
  "commands": [
    {"name": "apply", "title": "Apply", "description": "Applies a proposal.", "class": "internal",
     "input": {"type": "object", "properties": {"id": {"type": "integer"}, "relay": {"type": "string"}}},
     "result": {"type": "object"}, "executor": {"type": "script", "script": "apply"}}],
  "scripts": [{"name": "apply", "file": "scripts/apply.py", "runtime": "python"}],
  "triggers": [{"command": "apply", "cron": "0 9 * * *"}],
  "screens": [{"name": "main", "title": "Proposals", "blocks": [{"type": "table", "title": "Proposals", "source": {"collection": "proposals"},
    "columns": [{"label": "Models", "bind": "models"}],
    "actions": [{"label": "Apply", "command": "apply", "params": {"id": "=_id"},
      "allows": [{"command": "deck.assignment_set", "params": {"role": "text", "project": "=project", "command": "=command ?? ''", "models": "=split(models, ',')"}}]}]}]}],
  "menu": ["main"]}`
