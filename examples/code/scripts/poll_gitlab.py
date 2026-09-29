"""Make a card in Backlog for each new open issue of every followed GitLab project.

Input (stdin JSON): {} (a schedule).
Result (stdout JSON): {"cards": [ids]} or null when nothing is new.

Each project in the intake collection keeps a cursor: the created_at of the newest issue taken
and the iids taken at that very time, since GitLab's created_after includes it. After the Mac
slept the next poll takes everything since the cursor. Issues come through the gitlab_issues
tool, which signs in with the gitlab secret; this script never sees the token.
"""
import datetime
import json
import os
import sys
import urllib.error
import urllib.request

FIRST_DAYS = 14
TEXT_LIMIT = 4000


def deck(command, payload):
    req = urllib.request.Request(
        os.environ["DECK_URL"].rstrip("/") + "/commands/" + command,
        data=json.dumps(payload).encode(),
        headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"},
    )
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with opener.open(req, timeout=60) as r:
            return json.loads(r.read().decode() or "null")
    except urllib.error.HTTPError as e:
        raise SystemExit("%s: %s %s" % (command, e.code, e.read().decode()[:500]))


def issues(tool, project, after, labels):
    page = 1
    while True:
        got = deck(tool, {"project": project, "after": after, "labels": labels, "page": str(page)})
        if not isinstance(got, list):
            raise SystemExit("gitlab_issues: unexpected answer %r" % (got,))
        yield from got
        if len(got) < 50:
            return
        page += 1


own = deck("deck.manifest_get", {})["project"]
cards = []
errors = []
for rec in deck("deck.records_list", {"collection": "intake"}) or []:
    d = rec["data"]
    cursor = d.get("cursor") or (datetime.datetime.now(datetime.timezone.utc)
                                 - datetime.timedelta(days=FIRST_DAYS)).strftime("%Y-%m-%dT%H:%M:%SZ")
    seen = set(d.get("seen", [])) if d.get("cursor") else set()
    newest, at_newest = d.get("cursor", ""), set(seen)
    try:
        for issue in issues(own + ".gitlab_issues", d["project"], cursor, d.get("labels", "")):
            created, iid = issue["created_at"], issue["iid"]
            if d.get("cursor") and (created < d["cursor"] or (created == d["cursor"] and iid in seen)):
                continue
            body = (issue.get("description") or "")[:TEXT_LIMIT]
            labels = ", ".join(issue.get("labels") or [])
            text = "%s\n\nGitLab: %s%s" % (body, issue["web_url"], "\nLabels: " + labels if labels else "")
            card = deck("deck.card_create", {"title": "#%d %s" % (iid, issue["title"]), "text": text.strip()})
            cards.append(card["id"])
            if created > newest:
                newest, at_newest = created, {iid}
            elif created == newest:
                at_newest.add(iid)
            d["cursor"], d["seen"] = newest, sorted(at_newest)
            deck("deck.records_upsert", {"collection": "intake", "records": json.dumps(d)})
    except SystemExit as e:
        errors.append("%s: %s" % (d["project"], e))
if errors:
    print("\n".join(errors), file=sys.stderr)
    if not cards:
        raise SystemExit(1)
print(json.dumps({"cards": cards} if cards else None))
