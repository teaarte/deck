#!/usr/bin/env python3
"""Watches the real runs of each piece of work after the owner applied a proposal: for about three days it compares
the runs after the change (failures, timeouts, refusals of the model such as 429/413) with the runs before. When the work
got worse it writes a proposal to go back to the earlier models, ready for the owner's Apply, and one line of the feed."""
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


WATCH_DAYS = 3       # how long the runs after a change are watched
MAX_DAYS = 10        # a work that did not run by then is left: nothing to compare
BEFORE_DAYS = 14     # the runs before the change it is compared with
REFUSAL = ("413", "429", "rate limit", "Request too large", "tokens per minute")


def unhealthy(m):
    return m.get("status") == "error" or (m.get("status") == "warning" and m.get("verdict") == "model")


def runs_of(project, command):
    """The runs of a command of another project, or None when deck does not let this module read them."""
    try:
        rows = call("deck.processes_list", {"project": project, "command": command, "brief": True, "limit": 100,
                                            "fields": ["state", "error", "error_type", "provider", "started_at", "trial", "not_by_schema"]})
    except Failure as e:
        return None, str(e.details or e.error)[:200]
    return [r for r in rows or [] if not r.get("trial") and r.get("state") not in ("running", "waiting", "queued")], ""


def tally(rows, model):
    out = {"runs": len(rows), "failed": 0, "timeouts": 0, "refusals": 0, "on_model": 0, "failed_on_model": 0}
    for r in rows:
        bad = r.get("state") in ("error", "stopped") or r.get("not_by_schema")
        on = r.get("provider") == model
        out["on_model"] += on
        if not bad:
            continue
        out["failed"] += 1
        out["failed_on_model"] += on
        err = str(r.get("error") or "")
        if r.get("error_type") == "timeout" or (r.get("state") == "stopped" and on):
            out["timeouts"] += 1  # a run stopped while the model hung counts as a hang
        if r.get("error_type") == "model.limit" or any(s in err for s in REFUSAL):
            out["refusals"] += 1
    return out


def words(t):
    return (f"{t['runs']} запусков, ошибок {t['failed']}" + (f" (зависаний {t['timeouts']}" if t["timeouts"] else "")
            + (f", отказов модели {t['refusals']}" if t["refusals"] else "") + (")" if t["timeouts"] else ""))


def main(inp):
    today = datetime.date.today()
    deck = {m["name"]: m for m in (call("deck.models_list", {"fields": ["models"]}) or {}).get("models") or []}
    recs = call("deck.records_list", {"collection": "proposals", "where": "status == 'applied' && !(watch_done ?? false)"}) or []
    rows, notes, worse, blind = [], [], [], []
    for r in recs:
        d = dict(r["data"])
        model = d.get("proposed")
        start = d.get("watch_from") or d.get("ask_day") or d.get("day") or today.isoformat()
        prev = d.get("watch_prev") or d.get("current_models") or []
        d.update(watch_from=start, watch_prev=prev, watch_until=(datetime.date.fromisoformat(start) + datetime.timedelta(days=WATCH_DAYS)).isoformat())
        days = (today - datetime.date.fromisoformat(start)).days
        got, why = runs_of(d["project"], d.get("command") or "")
        if got is None:
            # deck does not show this module the runs of another project: only the health of the model is known
            blind.append(f"{d['target']}: {why[:160]}")
            m = deck.get(model) or {}
            if unhealthy(m):
                after, before = {"runs": 0, "failed": 0, "timeouts": 0, "refusals": 0, "on_model": 0, "failed_on_model": 0}, None
                d["watch_note"] = f"запуски проекта не видны модулю; {model} не проходит самопроверку deck"
                bad = True
            else:
                d["watch_note"] = "запуски проекта не видны модулю (нет права читать процессы), смотрю только здоровье модели"
                d["alert"] = "наблюдаю после замены"
                if days >= MAX_DAYS:
                    d.update(watch_done=True, alert="")
                rows.append(d)
                continue
        else:
            after_rows = [x for x in got if (x.get("started_at") or "")[:10] >= start]
            before_rows = [x for x in got if (datetime.date.fromisoformat(start) - datetime.timedelta(days=BEFORE_DAYS)).isoformat()
                           <= (x.get("started_at") or "")[:10] < start]
            after, before = tally(after_rows, model), tally(before_rows, model)
            rate_a = after["failed"] / after["runs"] if after["runs"] else 0
            rate_b = before["failed"] / before["runs"] if before["runs"] else 0
            bad = (after["refusals"] >= 1 and after["failed_on_model"] >= 1) or after["failed_on_model"] >= 2 \
                or (after["runs"] >= 2 and rate_a > rate_b + 0.25)
            d["watch_note"] = f"после замены: {words(after)}; до: {words(before)}"
            if after["runs"] and not after["on_model"]:
                d["watch_note"] += f"; {model} ещё ни разу не отвечал"
        d["watch_after"], d["watch_before"] = after, before
        if bad:
            keep = [n for n in prev if n in deck and not unhealthy(deck[n])]
            now = d.get("now_models") or [d.get("proposed")]
            d.update(watch_done=True, watch_verdict="worse")
            if keep and keep != now:
                d.update(status="ready", revert=True, keep_first=False, proposed=keep[0], proposed_key="", in_deck=True,
                         apply_models=",".join(keep), current=d.get("now") or model, current_models=now, test="ok",
                         test_note=("до замены: " + words(before)) if before else "до замены эта цепочка работала",
                         reason=f"После замены на {model} работа стала хуже: {d['watch_note']}. Предлагаю вернуть прежние модели.",
                         day=today.isoformat(), alert=f"после замены на {model} стало хуже")
                worse.append(f"{d['project']} · {d.get('title') or d.get('command')}")
            else:
                d["alert"] = f"после замены на {model} стало хуже, а прежние модели не работают: подберу другую"
            rows.append(d)
            continue
        ran = after["runs"] if got is not None else 0
        if days >= WATCH_DAYS and (ran or days >= MAX_DAYS):
            d.update(watch_done=True, watch_verdict="ok" if ran else "no_runs", alert="")
        else:
            d["alert"] = "наблюдаю после замены"
        rows.append(d)
    if rows:
        call("deck.records_upsert", {"collection": "proposals", "records": "\n".join(json.dumps(x, ensure_ascii=False) for x in rows)})
    if worse:
        call("deck.notify", {"text": "После замены модели стало хуже: " + ", ".join(worse)[:200]
                             + ". Предлагаю вернуть прежние — «Применить» в «Предложениях».", "screen": "proposals"})
    return {"watched": len(rows), "worse": worse, "blind": blind,
            "done": [x["target"] for x in rows if x.get("watch_done")]}


run(main)
