#!/usr/bin/env python3
"""Removes a model this module added to deck once it has lain broken and spare for a week: deck.models_list marks
spare a free model the module's own call added that no assignment names, which deck.model_delete removes without the
owner, who sees it with an undo. idle.json in the data folder keeps since when each lies broken and spare."""
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


IDLE_DAYS = 7  # days a model of the module lies broken and spare before it is removed
IDLE = os.path.join(os.environ["DECK_DATA"], "idle.json")


def main(inp):
    idle = {}
    if os.path.exists(IDLE):
        with open(IDLE) as f:
            idle = json.load(f)
    today = datetime.date.today().isoformat()
    deck = {m["name"]: m for m in (call("deck.models_list", {"fields": ["models"]}) or {}).get("models") or []}
    spare = {n: m for n, m in deck.items() if m.get("spare")}
    latest = {}  # the last failed test of each model: whole when the model itself failed, not only one work
    for x in call("deck.records_list", {"collection": "trials"}) or []:
        d = x["data"]
        if d.get("day", "") >= latest.get(d["model"], {}).get("day", ""):
            latest[d["model"]] = d
    passed = {}  # the last day a test of each model passed
    for x in call("deck.records_list", {"collection": "proposals", "where": "test == 'ok'"}) or []:
        d = x["data"]
        if d.get("proposed") and d.get("day", "") > passed.get(d["proposed"], ""):
            passed[d["proposed"]] = d["day"]
    for name, since in list(idle.items()):
        if name not in spare or passed.get(name, "") >= since:
            del idle[name]
    removed, asked, refused = [], [], []
    for name, m in spare.items():
        t = latest.get(name) or {}
        broken = m.get("status") == "error" or (t.get("whole") and t.get("day", "") > passed.get(name, ""))
        if not broken and name not in idle:
            continue
        since = idle.setdefault(name, today)
        if (datetime.date.fromisoformat(today) - datetime.date.fromisoformat(since)).days < IDLE_DAYS:
            continue
        try:
            got = call("deck.model_delete", {"name": name}) or {}
        except Failure as e:
            refused.append(f"{name}: {str(e.details or e.error)[:160]}")  # changed meanwhile: tried again next run
            continue
        (asked if isinstance(got, dict) and got.get("waiting") else removed).append(name)
        del idle[name]
    tmp = f"{IDLE}.{os.getpid()}.tmp"
    with open(tmp, "w") as f:
        json.dump(idle, f, ensure_ascii=False)
    os.replace(tmp, IDLE)
    return {"removed": removed, "asked": asked, "refused": refused}


run(main)
