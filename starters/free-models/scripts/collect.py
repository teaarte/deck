#!/usr/bin/env python3
"""Reads the free models of OpenRouter and NVIDIA and the cheap text models of OpenRouter, writes new ones as `new`,
marks vanished ones `gone`, returns the queue to rate."""
import json, os, sys, time, traceback, urllib.error, urllib.request, datetime

LISTED = ("openrouter", "nvidia")
# A cheap model: at most these USD per million input/output tokens; only models newer than RECENT_DAYS enter the list.
CHEAP_IN, CHEAP_OUT, RECENT_DAYS = 0.6, 3.0, 180


# --- deck helper (skills/module-builder/helpers/deck_helper.py) ---

class Failure(Exception):
    """A failure of the run: error is one line for the owner, type one deck takes (None lets deck type it)."""

    def __init__(self, error, type=None, details=None):
        super().__init__(error)
        self.error, self.type, self.details = error, type, details


def call(command, inp=None):
    """Calls a deck command and returns its result; an error answer raises Failure with deck's answer as details."""
    req = urllib.request.Request(os.environ["DECK_URL"] + "/commands/" + command, data=json.dumps(inp or {}).encode(),
                                 headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=3600) as r:
            body = r.read().decode()
    except urllib.error.HTTPError as e:
        # No type: deck types a failure by its own last error answer.
        raise Failure(f"{command}: deck answered {e.code}", details=e.read().decode(errors="replace")[:4000]) from None
    return json.loads(body) if body.strip() else None


def run(main):
    """The entry point: runs main, prints its result, and turns a failure nobody caught into the one form deck reads."""
    try:
        print(json.dumps(main(json.load(sys.stdin))))
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


def fetch(url):
    req = urllib.request.Request(url, headers={"User-Agent": "deck-free-models/1"})
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.loads(r.read().decode())


def openrouter():
    out = []
    for m in fetch("https://openrouter.ai/api/v1/models").get("data") or []:
        p = m.get("pricing") or {}
        free = m["id"].endswith(":free") or (str(p.get("prompt")) == "0" and str(p.get("completion")) == "0"
                                              and str(p.get("request", "0")) == "0" and str(p.get("image", "0")) == "0")
        arch = m.get("architecture") or {}
        rec = {"model_id": m["id"], "name": m.get("name") or m["id"], "modality": arch.get("modality") or "",
               "context": m.get("context_length") or 0, "tools": "tools" in (m.get("supported_parameters") or []),
               "url": "https://openrouter.ai/" + m["id"].split(":")[0]}
        if free:
            if m["id"] != "openrouter/free":
                out.append(dict(rec, tier="free"))
            continue
        try:
            pin, pout = float(p.get("prompt") or 0) * 1e6, float(p.get("completion") or 0) * 1e6
        except (TypeError, ValueError):
            continue
        # Cheap text models an agent can work with: tools, long context, no per-request fee. Routers price at -1,
        # ~...-latest are aliases and :batch the same model in batch mode: none of them is a model of its own.
        if m["id"].startswith("~") or m["id"].endswith(":batch") or pin < 0 or pout < 0:
            continue
        if (pin <= CHEAP_IN and pout <= CHEAP_OUT and str(p.get("request") or "0") == "0" and rec["tools"]
                and (arch.get("output_modalities") or ["text"]) == ["text"] and rec["context"] >= 64000
                and not m["id"].startswith("openrouter/")):
            out.append(dict(rec, tier="cheap", price_in=round(pin, 3), price_out=round(pout, 3),
                            created=m.get("created") or 0))
    return out


def nvidia():
    return [{"model_id": m["id"], "name": m["id"], "modality": "", "context": 0, "tools": False,
             "url": "https://build.nvidia.com/" + m["id"]}
            for m in fetch("https://integrate.api.nvidia.com/v1/models").get("data") or []]


def main(inp):
    today = datetime.date.today().isoformat()
    have = {r["key"]: r["data"] for r in call("deck.records_list", {"collection": "models"}) or []}
    found, errors = {}, []
    for src, fn in (("openrouter", openrouter), ("nvidia", nvidia)):
        try:
            for m in fn():
                found[f"{src}:{m['model_id']}"] = dict(m, source=src)
        except Exception as e:  # one source down must not stop the other
            errors.append(f"{src}: {e}")
    ok = {s for s in LISTED if not any(e.startswith(s + ":") for e in errors)}
    if not ok:  # no list read at all: the sites, not the data, are the problem
        raise Failure("neither OpenRouter nor NVIDIA answered", "network", errors)

    write, new, gone = [], [], []
    for key, m in found.items():
        cur = have.get(key)
        if cur is None:
            if m.get("tier") == "cheap" and time.time() - m.get("created", 0) > RECENT_DAYS * 86400:
                continue
            rec = dict(m, kind="other", status="new", listed=True, first_seen=today, last_seen=today)
            new.append(key)
        elif not cur.get("listed", True) or cur.get("status") == "gone":
            rec = dict(cur, listed=True, last_seen=today, status="new" if cur.get("status") == "gone" else cur["status"])
            if m.get("tier") == "cheap":
                rec.update(price_in=m["price_in"], price_out=m["price_out"])
        elif m.get("tier") == "cheap" and (cur.get("price_in"), cur.get("price_out")) != (m["price_in"], m["price_out"]):
            rec = dict(cur, price_in=m["price_in"], price_out=m["price_out"], last_seen=today)
        else:
            continue
        rec.pop("created", None)
        write.append(rec)
    for key, cur in have.items():
        # Only records collect wrote itself (they carry modality) can vanish from a list; review adds others.
        if cur.get("source") in ok and "modality" in cur and key not in found and cur.get("listed", True):
            write.append(dict(cur, listed=False, status="gone", last_seen=today))
            if (cur.get("score") or 0) >= 7:
                gone.append(cur.get("name") or key)
    if write:
        res = call("deck.records_upsert", {"collection": "models",
                                           "records": "\n".join(json.dumps(r, ensure_ascii=False) for r in write)})
        errors += res.get("errors") or []
    if gone:
        call("deck.notify", {"text": "Перестали быть бесплатными, подорожали или пропали: " + ", ".join(gone)[:250], "screen": "models"})

    state = {**have, **{f"{r['source']}:{r['model_id']}": r for r in write}}
    queue = [f"{r['source']} | {r['model_id']} | {r.get('name', '')} | {r.get('modality', '')} | {r.get('context', 0)} | "
             f"{'tools' if r.get('tools') else ''}"
             + (f" | ${r['price_in']}/${r['price_out']} per 1M" if r.get("tier") == "cheap" else "") for r in state.values() if r.get("status") == "new"]
    if not queue:  # nothing to rate: the review does not start
        return None
    return {"new": len(new), "gone": gone, "queue": queue, "errors": errors}


run(main)
