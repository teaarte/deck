#!/usr/bin/env python3
"""Removes a model this module added to deck once it is broken and no work has used it for a week: deck.model_delete
removes a free model the module's own call added that no assignment names, and the owner sees it with an undo.
own.json in the data folder keeps the models the module asked deck to add and since when each lies broken and unused."""
import datetime, json, os, sys, traceback, urllib.error, urllib.request
# --- deck helper (skills/module-builder/helpers/deck_helper.py) ---

class Failure(Exception):
    """A failure of the run: error is one line for the owner, type one deck takes (None lets deck type it)."""

    def __init__(self, error, type=None, details=None):
        super().__init__(error)
        self.error, self.type, self.details = error, type, details


def call(command, inp=None, timeout=3600):
    """Calls a deck command and returns its result; an error answer raises Failure with deck's answer as details."""
    req = urllib.request.Request(os.environ["DECK_URL"] + "/commands/" + command, data=json.dumps(inp or {}).encode(),
                                 headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            body = r.read().decode()
    except urllib.error.HTTPError as e:
        # No type: deck types a failure by its own last error answer.
        raise Failure(f"{command}: deck answered {e.code}", details=e.read().decode(errors="replace")[:4000]) from None
    return json.loads(body) if body.strip() else None


def run(main):
    """The entry point: runs main, prints its result, and turns a failure nobody caught into the one form deck reads."""
    try:
        print(json.dumps(main(json.load(sys.stdin)), ensure_ascii=False))
    except Failure as f:
        out = {"error": f.error}
        if f.type:
            out["type"] = f.type
        if f.details is not None:
            out["details"] = f.details
        print(json.dumps(out, default=str))
        sys.exit(1)
    except Exception as e:
        print(json.dumps({"error": f"{type(e).__name__}: {e}"[:300], "details": traceback.format_exc()[-4000:]}))
        sys.exit(1)

# --- end of the helper ---


IDLE_DAYS = 7     # days a model of the module lies broken and unused before it is removed
FORGET_DAYS = 30  # a model the owner never added is forgotten after this many days
OWN = os.path.join(os.environ["DECK_DATA"], "own.json")


def unhealthy(m):
    return m.get("status") == "error" or (m.get("status") == "warning" and m.get("verdict") == "model")


def free(m):
    """Costs nothing: free, or with no price and no subscription, as deck counts it."""
    return m.get("free") or not (m.get("subscription") or any(m.get(k) is not None for k in ("input_usd", "output_usd", "call_usd")))


def in_use():
    """Models some work or assignment names, of every project compare keeps in work.json: deck shows a module the
    assignments of another project only when asked for that project."""
    path = os.path.join(os.environ["DECK_DATA"], "work.json")
    kept = []
    if os.path.exists(path):
        with open(path) as f:
            kept = json.load(f)
    used = set()
    for p in sorted({r.get("project") for r in kept if r.get("project")}):
        try:
            got = call("deck.models_list", {"project": p, "fields": ["assignments", "work"]}) or {}
            rows = (got.get("assignments") or []) + (got.get("work") or [])
        except Failure:
            rows = [r for r in kept if r.get("project") == p]
        for r in rows:
            used |= set(r.get("models") or []) | ({r["model"]} if r.get("model") else set())
    return used


def save(own):
    tmp = f"{OWN}.{os.getpid()}.tmp"
    with open(tmp, "w") as f:
        json.dump(own, f, ensure_ascii=False)
    os.replace(tmp, OWN)


def main(inp):
    if not os.path.exists(OWN):
        return {"removed": []}
    with open(OWN) as f:
        own = json.load(f)
    today = datetime.date.today()
    deck = {m["name"]: m for m in (call("deck.models_list", {"fields": ["models"]}) or {}).get("models") or []}
    trials = call("deck.records_list", {"collection": "trials", "where": "whole ?? false"}) or []
    used = in_use()
    failed = {x["data"]["model"] for x in trials if x["data"].get("until", "") >= today.isoformat()}
    removed, asked, refused = [], [], []
    for name, o in list(own.items()):
        m = deck.get(name)
        if not m:
            if (today - datetime.date.fromisoformat(o.get("asked") or today.isoformat())).days >= FORGET_DAYS:
                del own[name]
            continue
        idle = free(m) and not m.get("used") and not m.get("projects") and name not in used and (unhealthy(m) or name in failed)
        if not idle:
            o.pop("idle_since", None)
            continue
        since = o.setdefault("idle_since", today.isoformat())
        if (today - datetime.date.fromisoformat(since)).days < IDLE_DAYS:
            continue
        try:
            got = call("deck.model_delete", {"name": name}) or {}
        except Failure as e:
            refused.append(f"{name}: {str(e.details or e.error)[:160]}")  # changed meanwhile: tried again next run
            continue
        if isinstance(got, dict) and got.get("waiting"):
            asked.append(name)  # deck took it for no model of this module: the owner decides, the module asks no more
        else:
            removed.append(name)
        del own[name]
    save(own)
    return {"removed": removed, "asked": asked, "refused": refused}


run(main)
