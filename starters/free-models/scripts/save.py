#!/usr/bin/env python3
"""Writes the verdicts of review into models, marks the rest of the queue skipped, tells the owner about new strong models."""
import json, os, re, sys, traceback, urllib.error, urllib.request, datetime

KINDS = ("llm", "image", "video", "tts", "stt", "music", "embed", "other")


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


# code, quotes, links, :free and ids such as flux.1-dev, sk_ or build.nvidia.com are no English words
CODE = re.compile(r"`[^`]*`|«[^»]*»|https?://\S+|:[a-z]+\b|[\w.:/@~+-]*(?:[\d_/@]|[a-z]\.[a-z])[\w.:/@~+-]*", re.A)


def russian(s):
    """Plain Russian: Cyrillic text without lowercase English words (names and terms are capitalised)."""
    if not s:
        return True
    return len(re.findall(r"[а-яё]", s, re.I)) >= 10 and not re.findall(r"\b[a-z]{3,}\b", CODE.sub(" ", s)) \
        and not re.search(r"[一-鿿぀-ヿ]", s)


def text_model(mid, modality):
    """A model that answers text, also one that reads pictures (vision-language); not a guard, embedder or router."""
    if re.search(r"guard|moderat|embed|rerank|router", mid, re.I):
        return False
    return bool(re.search(r"->text$", modality or "") or re.search(r"(^|[-/])vl([-:]|$)|vision", mid, re.I))


def upsert(rows):
    if not rows:
        return {"ids": [], "errors": []}
    return call("deck.records_upsert", {"collection": "models",
                                        "records": "\n".join(json.dumps(r, ensure_ascii=False) for r in rows)})


def num(v, lo, hi):
    try:
        return max(lo, min(hi, float(v)))
    except (TypeError, ValueError):
        return None


def main(inp):
    res = (call("deck.process_get", {"process": inp["process"]}) or {}).get("result") or {}
    today = datetime.date.today().isoformat()
    have = {r["key"]: r["data"] for r in call("deck.records_list", {"collection": "models"}) or []}
    rows, seen, named = [], set(), []
    for m in res.get("models") or []:
        src = str(m.get("source") or "").strip().lower().replace(" ", "-")
        mid = str(m.get("model_id") or "").strip()
        if not src or not mid:
            continue
        key = f"{src}:{mid}"
        if key in seen:
            continue
        seen.add(key)
        cur = have.get(key, {})
        was_strong = cur.get("status") == "rated" and (cur.get("score") or 0) >= 7
        rec = dict(cur)
        for k in ("name", "strengths", "limits", "secret", "how_to", "url"):
            if m.get(k):
                rec[k] = str(m[k])
        roles = m.get("roles") or []
        rec["roles"] = [str(x) for x in (roles if isinstance(roles, list) else [roles]) if x]
        rec["kind"] = m.get("kind") if m.get("kind") in KINDS else rec.get("kind", "other")
        if rec["kind"] == "other" and text_model(mid, rec.get("modality")):
            rec["kind"] = "llm"
        bad = [k for k in ("strengths", "limits") if not russian(rec.get(k))]
        for k in bad:  # a garbled or English text never replaces a good one
            if cur.get(k) and russian(cur[k]):
                rec[k] = cur[k]
        rec["text_ok"] = all(russian(rec.get(k)) for k in ("strengths", "limits"))
        rec["score"] = num(m.get("score"), 2, 10) or 2
        c = num(m.get("confidence"), 0, 1)
        if c is not None:
            rec["confidence"] = c
        rec.update(source=src, model_id=mid, status="rated", rated=today, last_seen=today)
        rec.setdefault("name", mid)
        rec.setdefault("first_seen", today)
        rec.setdefault("listed", True)
        rows.append(rec)
        if rec["score"] >= 7 and not was_strong:
            named.append(rec["name"])
    for key, cur in have.items():
        if cur.get("status") == "new" and key not in seen:
            rows.append(dict(cur, status="skipped", rated=today))
        elif cur.get("status") == "rated" and key not in seen:  # older verdicts too: review rewrites the flagged ones
            ok = all(russian(cur.get(k)) for k in ("strengths", "limits"))
            if cur.get("text_ok") != ok:
                rows.append(dict(cur, text_ok=ok))
    errors = list(upsert(rows).get("errors") or [])
    if named:
        call("deck.owner_notify", {"text": f"Новые сильные бесплатные модели ({len(named)}): " + ", ".join(named)[:240],
                             "screen": "models"})
    if res.get("summary"):
        call("deck.records_upsert", {"collection": "digests", "records": json.dumps(
            {"day": today, "text": res["summary"], "rated": len(seen), "cards": len(named)}, ensure_ascii=False)})
    return {"rated": len(seen), "skipped": len(rows) - len(seen), "new_strong": len(named), "errors": errors}


run(main)
