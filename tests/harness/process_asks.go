package harness

// PressScript makes the deck calls of its input, one or many, and prints deck's last answer as its result; with n it
// only names ask n as the one its work waits for.
const PressScript = `import json, os, sys, urllib.request, urllib.error
inp = json.load(sys.stdin)
if inp.get("n"):
    print(json.dumps({"waiting": inp["n"]}))
    sys.exit(0)
calls = json.loads(inp["form"]) if inp["method"] == "many" else [{"method": inp["method"], "form": json.loads(inp["form"])}]
body = "{}"
for c in calls:
    req = urllib.request.Request(os.environ["DECK_URL"] + "/commands/" + c["method"], data=json.dumps(c["form"]).encode(),
        headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
    try:
        body = urllib.request.urlopen(req, timeout=30).read().decode()
    except urllib.error.HTTPError as e:
        body = json.dumps({"status": e.code, "body": e.read().decode()})
print(body if body.strip() else "{}")
`

type PressAsk struct {
	ID    int64          `json:"id"`
	Kind  string         `json:"kind"`
	What  string         `json:"what"`
	Where string         `json:"where"`
	Allow map[string]any `json:"allow"`
	Deny  map[string]any `json:"deny"`
}
