"""Pick postings that have no rating yet and hand them to the rating agent as short excerpts.

Input (stdin JSON): {"limit": 150} optional; "process" from the after-trigger is ignored.
Result: {"items": [...], "left": n, "source_errors": [...]} or null when nothing waits and no source failed.
The agent never has to list the whole collection, and never touches the full records.
"""
import json
import os
import sys
import urllib.request

EXCERPT = 1200
DEFAULT_LIMIT = 150


def deck(command, payload):
    url = os.environ["DECK_URL"].rstrip("/") + "/commands/deck." + command
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode(),
        headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"],
                 "Content-Type": "application/json"},
    )
    errors = []
    for opener in (urllib.request.build_opener(urllib.request.ProxyHandler({})),
                   urllib.request.build_opener()):
        try:
            with opener.open(req, timeout=60) as r:
                body = r.read().decode()
            return json.loads(body) if body.strip() else None
        except Exception as e:  # noqa: BLE001
            errors.append(repr(e))
    raise RuntimeError("deck.%s failed: %s" % (command, "; ".join(errors)))


def records(collection):
    data = deck("records_list", {"collection": collection}) or []
    return [(r["id"], r.get("data") or {}) for r in data]


def main():
    raw = sys.stdin.read().strip()
    inp = json.loads(raw) if raw else {}
    limit = int(inp.get("limit") or DEFAULT_LIMIT)

    pending = [(i, d) for i, d in records("postings") if not d.get("rating")]
    pending.sort(key=lambda x: x[0])
    batch = pending[:limit]
    items = []
    for i, d in batch:
        items.append({
            "id": i,
            "url": d.get("url"),
            "company": d.get("company", ""),
            "role": d.get("role", ""),
            "location": d.get("location", ""),
            "remote": d.get("remote", False),
            "salary": d.get("salary", ""),
            "source": d.get("source_name", ""),
            "excerpt": (d.get("text") or "")[:EXCERPT],
        })
    errors = ["%s: %s" % (d.get("name") or d.get("url"), d.get("last_error"))
              for _, d in records("sources") if d.get("status") == "error"]
    print("%d postings wait for a rating, %d in this batch" % (len(pending), len(items)), file=sys.stderr)
    if not items and not errors:
        print("null")
        return
    print(json.dumps({"items": items, "left": len(pending) - len(items), "source_errors": errors},
                     ensure_ascii=False))


if __name__ == "__main__":
    main()
