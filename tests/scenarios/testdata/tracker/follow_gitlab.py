"""Start following a GitLab project: its open issues arrive as tasks in backlog.

Input (stdin JSON): {"project": "group/name", "labels": "a,b"}; labels are optional.
Result (stdout JSON): {"project": "group/name"}.

The first poll takes the issues of the last 14 days. A project followed again keeps its cursor, so new
labels apply to issues created after it.
"""
import json
import os
import sys
import urllib.request


def deck(command, payload):
    req = urllib.request.Request(
        os.environ["DECK_URL"].rstrip("/") + "/commands/" + command,
        data=json.dumps(payload).encode(),
        headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"},
    )
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(req, timeout=60) as r:
        return json.loads(r.read().decode() or "null")


inp = json.load(sys.stdin)
project = inp["project"].strip().strip("/")
old = {}
for r in deck("deck.records_list", {"collection": "intake"}) or []:
    if r["data"].get("project") == project:
        old = r["data"]
record = {"project": project, "labels": inp.get("labels", "").strip()}
if old.get("cursor"):
    record["cursor"] = old["cursor"]
    record["seen"] = old.get("seen", [])
out = deck("deck.records_upsert", {"collection": "intake", "records": json.dumps(record)})
if out.get("errors"):
    raise SystemExit("intake: %s" % out["errors"])
print(json.dumps({"project": project}))
