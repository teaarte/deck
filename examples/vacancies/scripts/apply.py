"""Write the ratings of a finished `rate` process into the postings and put fits/maybe on the board.

Input (stdin JSON): {"process": <rate process id>}.
Result: {"updated": n, "cards": [...]} or null when the rating had nothing to apply.
"""
import datetime
import json
import os
import sys
import urllib.request


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


FIXABLE = ("company", "role", "location", "remote", "salary")


def card_text(d, r):
    lines = ["**%s** (confidence %.2f) — %s" % (r["choice"], r.get("confidence", 0), r.get("reason", "")), ""]
    facts = [d.get("location") or "", "remote" if d.get("remote") else "", d.get("salary") or ""]
    facts = [f for f in facts if f]
    if facts:
        lines.append(" · ".join(facts))
    lines.append("[Posting](%s) — %s" % (d["url"], d.get("source_name") or d.get("source")))
    return "\n".join(lines)


def main():
    raw = sys.stdin.read().strip()
    inp = json.loads(raw) if raw else {}
    proc = deck("process_get", {"process": inp["process"]}) or {}
    result = proc.get("result") or {}
    ratings = result.get("ratings") or []
    if not ratings:
        print("null")
        return

    postings = {r["id"]: r.get("data") or {} for r in deck("records_list", {"collection": "postings"}) or []}
    today = datetime.date.today().isoformat()
    updated, cards, skipped = [], [], []
    for r in ratings:
        d = postings.get(r.get("id"))
        if d is None:
            skipped.append(r.get("id"))
            continue
        for f in FIXABLE:
            v = (r.get("fixes") or {}).get(f)
            if v not in (None, ""):
                d[f] = v
        d["rating"] = {
            "choice": r["choice"],
            "probabilities": r["probabilities"],
            "confidence": r["confidence"],
            "reason": r.get("reason", ""),
            "rated": today,
        }
        if r["choice"] in ("fits", "maybe") and not d.get("card"):
            title = "%s — %s" % (d.get("company") or "?", d.get("role") or "?")
            card = deck("card_create", {"lane": "found", "record": r["id"], "title": title[:200],
                                        "text": card_text(d, d["rating"])})
            cid = card.get("id") if isinstance(card, dict) else card
            if cid:
                d["card"] = cid
                cards.append(cid)
        updated.append(d)

    if updated:
        deck("records_upsert", {"collection": "postings",
                                "records": "\n".join(json.dumps(d, ensure_ascii=False) for d in updated)})
    print("applied %d ratings, %d new cards, %d unknown ids" % (len(updated), len(cards), len(skipped)),
          file=sys.stderr)
    print(json.dumps({"updated": len(updated), "cards": cards, "unknown_ids": skipped}))


if __name__ == "__main__":
    main()
