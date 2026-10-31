#!/usr/bin/env python3
"""Asks the owner for one change of deck's models by a button: a ready proposal becomes a call of
deck.assignment_set, a model of the list deck lacks a call of deck.model_set; each waits for the owner's yes."""
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


def main(inp):
    r = call("deck.record_get", {"record": inp["id"]}) or {}
    d = r.get("data") or {}
    if d.get("target"):  # a proposal: assign its chain
        if d.get("status") not in ("ready", "asked"):
            raise Failure("предложение не готово к применению", type="input.invalid", details={"status": d.get("status")})
        body = {"role": d["role"], "project": d["project"], "models": [m for m in (d.get("apply_models") or "").split(",") if m]}
        if d.get("command"):
            body["command"] = d["command"]
        got = call("deck.assignment_set", body) or {}
        if isinstance(got, dict) and got.get("waiting"):
            d = dict(d, status="asked", test="asked", ask=got["waiting"], ask_day=datetime.date.today().isoformat())
            call("deck.record_update", {"record": inp["id"], "data": json.dumps(d, ensure_ascii=False)})
            return {"waiting": got["waiting"]}
        call("deck.record_update", {"record": inp["id"], "data": json.dumps(dict(d, status="applied"), ensure_ascii=False)})
        return {"applied": True}
    if d.get("model_id"):  # a model of the list: add it to deck
        kind = "text" if d.get("kind") in ("llm", "other", None) else d["kind"]
        got = call("deck.model_set", {"connection": d["source"], "model": d["model_id"], "kind": kind}) or {}
        return {"waiting": got.get("waiting")} if isinstance(got, dict) and got.get("waiting") else {"added": True}
    raise Failure("это не предложение и не модель", type="input.invalid", details={"record": inp["id"]})


run(main)
