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


def card(issue):
    body = (issue.get("description") or "")[:TEXT_LIMIT]
    labels = ", ".join(issue.get("labels") or [])
    text = "%s\n\nGitLab: %s%s" % (body, issue["web_url"], "\nLabels: " + labels if labels else "")
    return deck("deck.card_create", {"title": "#%d %s" % (issue["iid"], issue["title"]), "text": text.strip()})["id"]


def poll(tool, d):
    """Takes the new issues of one followed project, oldest first, and moves its cursor after each card.

    Every page is asked again from the moved cursor: an issue closed meanwhile does not shift an unseen one
    past a page boundary. A full page with nothing new (all at the cursor's second) goes on to the next page.
    """
    cursor = d.get("cursor") or (datetime.datetime.now(datetime.timezone.utc)
                                 - datetime.timedelta(days=FIRST_DAYS)).strftime("%Y-%m-%dT%H:%M:%S.000Z")
    seen = set(d.get("seen", []))
    made, page = [], 1
    while True:
        got = deck(tool, {"project": d["project"], "after": cursor, "labels": d.get("labels", ""), "page": str(page)})
        if not isinstance(got, list):
            raise SystemExit("gitlab_issues: unexpected answer %r" % (got,))
        new = [i for i in got if i["created_at"] > cursor or (i["created_at"] == cursor and i["iid"] not in seen)]
        for issue in sorted(new, key=lambda i: (i["created_at"], i["iid"])):
            made.append(card(issue))
            if issue["created_at"] > cursor:
                cursor, seen = issue["created_at"], set()
            seen.add(issue["iid"])
            d["cursor"], d["seen"] = cursor, sorted(seen)
            out = deck("deck.records_upsert", {"collection": "intake", "records": json.dumps(d)})
            if out.get("errors"):
                raise SystemExit("intake: %s" % out["errors"])
        if len(got) < 50:
            return made
        page = 1 if new else page + 1


own = deck("deck.manifest_get", {})["project"]
cards = []
errors = []
for rec in deck("deck.records_list", {"collection": "intake"}) or []:
    try:
        cards += poll(own + ".gitlab_issues", rec["data"])
    except SystemExit as e:
        errors.append("%s: %s" % (rec["data"].get("project"), e))
if errors:
    # The cards already made stay; the failure shows as the state of the process and in its health.
    raise SystemExit("\n".join(errors) + ("\ncards made before the failure: %s" % cards if cards else ""))
print(json.dumps({"cards": cards} if cards else None))
