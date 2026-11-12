#!/usr/bin/env python3
"""Tests each model the plan proposed on a sample of its kind of work (for an agent: several tool turns over records
as large as the real command reads, then a JSON answer that needs every result; JSON for text; a picture, speech),
judges the answer by fixed checks, proposes models deck lacks with deck.model_set and writes the proposals; only those
that passed are ready for the owner. Models the owner rejected for a piece of work are never proposed for it again."""
import datetime, json, os, random, re, sys, time, traceback, urllib.error, urllib.request

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


MAX_TESTS = 10
MAX_ASKS = 2  # models proposed to deck in one run, so the owner's buttons do not pile up
CLI = {"claude-code", "codex"}
TOOL = {"type": "function", "function": {
    "name": "records_list", "description": "Lists records of a collection of deck, newest last.",
    "parameters": {"type": "object", "required": ["collection"], "properties": {
        "collection": {"type": "string", "description": "Collection name"},
        "where": {"type": "string", "description": "Only the records this expression is true for, such as status == 'pending'"},
        "limit": {"type": "integer", "description": "At most this many, the newest"}}}}}
MAX_TURNS = 6
COOLDOWN = (3, 7, 14)  # days a model is not proposed again for a work after its 1st, 2nd, later failed test
LIM = {}  # limits.json of the module: free-tier limits of models and the size of each piece of work


def need_bytes(target, role="agent"):
    """Bytes of tool results the real command reads in one run (limits.json, measured in its runs)."""
    kind = "agent" if role == "agent" else "text"
    return (LIM.get("work") or {}).get(target) or (LIM.get("work_default") or {}).get(kind) or (60000 if kind == "agent" else 8000)


def need_tokens(target, role):
    kind = "agent" if role == "agent" else "text"
    return int(need_bytes(target, role) / (LIM.get("bytes_per_token") or 3)) + int((LIM.get("overhead_tokens") or {}).get(kind) or 0)


def limits_of(name, conn):
    return dict((LIM.get("connections") or {}).get(conn) or {}, **((LIM.get("models") or {}).get(name) or {}))


def fit(name, conn, target, role):
    """None when the real size of the work fits the model's tokens a minute and context, else why not."""
    if role not in ("agent", "text"):
        return None
    l, need = limits_of(name, conn), need_tokens(target, role)
    if l.get("tpm") and need > l["tpm"]:
        return f"does not fit: the work takes ~{need // 1000}k tokens a request, the limit is {l['tpm']} tokens a minute ({l.get('why') or conn})"
    if l.get("context") and need > l["context"] * 0.8:
        return f"does not fit: the work takes ~{need // 1000}k tokens, the model's context is {l['context'] // 1000}k"
    return None


def whole(note):
    """A failure of the model itself (hangs, 5xx, gone), not of this work: it cools down for every work."""
    return bool(re.search(r"timed out|Timeout|did not answer|error (5\d\d|404)", note or ""))
TEXT = ("A short note of the owner: \"Bought a used MacBook Air M2 for 6500 crowns, battery 91%, a scratch on the lid. "
        "The seller threw in the charger and a case.\" Answer with JSON only, no comments: {\"summary\": \"one sentence in "
        "Russian\", \"score\": a whole number from 1 to 10, how good a buy it was}.")


def chat(conn, model, body, timeout=150):
    """One chat completion through deck's models ($DECK_MODELS); returns (answer, seconds) or raises Failure."""
    body = dict(body, model=model)
    req = urllib.request.Request(os.environ["DECK_MODELS"].rstrip("/") + f"/{conn}/chat/completions",
                                 data=json.dumps(body).encode(),
                                 headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json",
                                          # Cloudflare in front of some APIs (Groq) refuses Python's own agent: error 1010
                                          "User-Agent": "deck-free-models/1.0"})
    t = time.time()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return json.loads(r.read().decode()), time.time() - t
    except urllib.error.HTTPError as e:
        raise Failure(f"{e.code}", details=e.read().decode(errors="replace")[:300]) from None


def judge_json(text):
    """Pass when the answer is the JSON asked for: a Russian summary and a whole score of 1–10."""
    s = (text or "").strip()
    s = re.sub(r"^```(?:json)?|```$", "", s, flags=re.M).strip()
    m = re.search(r"\{.*\}", s, re.S)
    try:
        d = json.loads(m.group(0) if m else s)
    except Exception:
        return False, "the answer is not JSON"
    summ, score = d.get("summary"), d.get("score")
    if not isinstance(summ, str) or not re.search(r"[\u0400-\u04ff]", summ):
        return False, "summary not in Russian"
    if re.search(r"[一-鿿぀-ヿ]", summ):
        return False, "foreign hieroglyphs in the text"
    if not isinstance(score, int) or not 1 <= score <= 10:
        return False, "score is not a whole number 1–10"
    return True, f"{summ} (score {score})"


def test_text(m):
    a, sec = chat(m["connection"], m["model"], {"messages": [{"role": "user", "content": TEXT}], "max_tokens": 1500})
    msg = ((a.get("choices") or [{}])[0].get("message") or {})
    ok, note = judge_json(msg.get("content"))
    return ok, sec, note


def fixture(target):
    """Searches and pending listings of an olx-like task as large as the real work: the listings carry real text of
    the models list as their descriptions. One listing fits the search under its price limit, and its code word is the
    answer; the limit is only in the search, so the agent needs both results at once."""
    recs = call("deck.records_list", {"collection": "models", "fields": ["name", "strengths", "limits", "how_to"],
                                      "where": "status == 'rated'"}) or []
    filler = [" ".join(str(v) for v in (r.get("data") or {}).values() if v) for r in recs] or ["listing text"]
    rnd = random.Random()
    limit = rnd.randrange(1300, 1900, 10)
    code = f"KOD-{rnd.randrange(1000, 9999)}"
    searches = [
        {"id": 5481, "slug": "nas-nvme", "spec": f"A compact NAS for M.2 NVMe, 4 slots or more, used, up to {limit} zł. " + filler[0][:1500]},
        {"id": 5482, "slug": "monitor-4k", "spec": "A 4K monitor, 27–32 inch, IPS, used, up to 900 zł. " + filler[-1][:1500]},
        {"id": 5483, "slug": "ebike", "spec": "A city e-bike, frame M, used, up to 3500 zł. " + filler[len(filler) // 2][:1500]}]
    n, per = 20, max(1500, (need_bytes(target) - 6000) // 20)
    hit = rnd.randrange(5, n - 3)
    listings, k = [], 0
    for i in range(n):
        text = ""
        while len(text.encode()) < per:
            text += filler[k % len(filler)] + "\n\n"
            k += 1
        text = text.encode()[:per].decode(errors="ignore")
        rid = 12700 + i
        if i == hit:
            listings.append({"id": rid, "search": "nas-nvme", "title": "Beelink ME mini, 6×M.2 NVMe, N150", "price": limit - 150,
                             "status": "pending", "description": f"For sale, as new. Listing code {code}.\n\n" + text})
        elif i % 4 == 0:
            listings.append({"id": rid, "search": "nas-nvme", "title": f"TerraMaster F8 SSD #{i}", "price": limit + 200 + i * 10,
                             "status": "pending", "description": f"Listing code KOD-{rnd.randrange(1000, 9999)}.\n\n" + text})
        else:
            listings.append({"id": rid, "search": rnd.choice(["monitor-4k", "ebike"]), "title": f"Listing {i}",
                             "price": rnd.randrange(200, 5000), "status": "pending", "description": text})
    return {"searches": searches, "listings": listings}, listings[hit]["id"], limit, code


def test_agent(m, target):
    """An agent at the size of its real work: it must read the searches and the listings with the tool, keep both
    results, not repeat its calls, and answer JSON with the one fitting listing, the limit and its code."""
    data, want_id, limit, code = fixture(target)
    msgs = [{"role": "system", "content": "You are an agent of deck. Use the tools to get data; do not guess. "
                                          "Answer with JSON only when you have what you need."},
            {"role": "user", "content": "Take the searches (collection searches) and the pending listings (collection listings, "
                                        "status == 'pending'). Find the listing of the search nas-nvme priced at most the limit in "
                                        "the spec of that search. Answer with JSON only: {\"id\": the listing id, \"limit\": "
                                        "the search's limit in zł as a number, \"code\": the code from the listing's description}."}]
    seen, sec, size = set(), 0.0, 0
    for turn in range(MAX_TURNS):
        a, s = chat(m["connection"], m["model"], {"messages": msgs, "tools": [TOOL], "tool_choice": "auto", "max_tokens": 3000})
        sec += s
        used, tpm = (a.get("usage") or {}).get("prompt_tokens") or 0, limits_of(m["name"], m["connection"]).get("tpm")
        if tpm and used > tpm:
            return False, sec, f"the request of {used} tokens is over the limit of {tpm} tokens a minute: on the real work the model will refuse (413)"
        msg = (a.get("choices") or [{}])[0].get("message") or {}
        calls = msg.get("tool_calls") or []
        if not calls:
            if turn == 0:
                return False, sec, "did not call the tool"
            s2 = re.sub(r"^```(?:json)?|```$", "", (msg.get("content") or "").strip(), flags=re.M).strip()
            mm = re.search(r"\{.*\}", s2, re.S)
            try:
                d = json.loads(mm.group(0) if mm else s2)
            except Exception:
                return False, sec, "the answer is not JSON"
            got = (d.get("id"), d.get("limit"), str(d.get("code") or "").strip())
            if str(got[0]) != str(want_id):
                return False, sec, f"wrong listing ({got[0]} instead of {want_id}): lost the tool's results"
            if str(got[1]).replace(" ", "") != str(limit) or got[2] != code:
                return False, sec, f"wrong limit or code ({got[1]}, {got[2]}): lost the tool's result"
            return True, sec, f"{turn + 1} turns, {size // 1000} KB of data: found the listing, the limit and the code right"
        msgs.append({"role": "assistant", "content": msg.get("content") or "", "tool_calls": calls})
        for c in calls:
            f = c.get("function") or {}
            try:
                args = json.loads(f.get("arguments") or "{}")
            except Exception:
                return False, sec, "the call's arguments are not JSON"
            key = (f.get("name"), args.get("collection"))
            if key in seen:
                return False, sec, f"looped: called {key[0]} {key[1]} again though it already had the result"
            seen.add(key)
            rows = data.get(args.get("collection")) if f.get("name") == "records_list" else None
            out = json.dumps(rows if rows is not None else {"error": "no such collection or tool"}, ensure_ascii=False)
            size += len(out.encode())
            msgs.append({"role": "tool", "tool_call_id": c.get("id"), "content": out})
    return False, sec, f"did not answer in {MAX_TURNS} turns"


def test_media(m, kind):
    t = time.time()
    inp = {"kind": kind, "model": m["name"]}
    if kind == "image":
        inp["prompt"] = "a small red apple on a white table, soft daylight photo"
    else:
        inp.update(text="Hi! This is a short voice check.", prompt="Calm and clear.")
    r = call("deck.model_call", inp, timeout=300) or {}
    sec = time.time() - t
    if isinstance(r, dict) and r.get("waiting"):
        return None, sec, "waits for the owner's permission"
    files = r.get("files") or ([r] if r.get("file") else [])
    return (True, sec, f"file ready ({kind})") if files else (False, sec, "the model returned no file")


def run_test(m, role, target=""):
    if role == "agent":
        return test_agent(m, target)
    if role == "text":
        return test_text(m)
    if role in ("image", "tts"):
        return test_media(m, role)
    return None, 0, "this kind is checked by hand"


def main(inp):
    try:
        LIM.update(json.loads((call("deck.document_get", {"project": "free-models", "name": "limits.json", "fields": ["text"]}) or {}).get("text") or "{}"))
    except (Failure, ValueError):
        pass
    pr = call("deck.process_get", {"process": inp["process"], "fields": ["result"]}) or {}
    res = pr.get("result") or {}
    if not res.get("proposals"):
        return None
    with open(os.path.join(os.environ["DECK_DATA"], "slice.json")) as f:
        sl = json.load(f)
    targets = {t["id"]: t for t in sl.get("targets") or []}
    deck = {m["name"]: m for m in (call("deck.models_list", {"fields": ["models"]}) or {}).get("models") or []}
    old = {r["data"]["target"]: r["data"] for r in call("deck.records_list", {"collection": "proposals"}) or []}
    trials = {(x["data"]["target"], x["data"]["model"]): x["data"] for x in call("deck.records_list", {"collection": "trials"}) or []}
    today = datetime.date.today()
    asked = {d.get("proposed") for d in old.values() if d.get("asked")}
    tests, rows, refused, asks = {}, [], [], 0
    for p in res["proposals"]:
        t = targets.get(p.get("target"))
        cand = next((c for c in (t or {}).get("candidates") or [] if c["id"] == p.get("proposed")), None)
        prev = old.get(p.get("target")) or {}
        no = list(prev.get("rejected_models") or []) + ([prev["proposed"]] if prev.get("status") == "rejected" else [])
        if not t or not cand or cand["id"] in no:
            refused.append(p.get("target"))
            continue  # not a piece of work or a model the compare offered, or one the owner rejected: a slip of the agent
        keep, chain = t.get("keep_first"), t["chain"]
        rest = [n for n in chain if n != cand["id"] and n not in t.get("broken", [])]
        # the owner keeps Claude first on such work: the proposed model goes right after it
        apply = [t["using"], cand["id"]] + [n for n in rest if n != t["using"]] if keep else [cand["id"]] + rest
        row = {"target": t["id"], "project": t["project"], "command": t["command"], "title": t["title"], "role": t["role"],
               "current": t["using"], "current_models": chain, "current_price": t.get("price") or "",
               "proposed": cand["id"], "proposed_key": f"{cand['connection']}:{cand['model_id']}",
               "proposed_price": cand.get("price") or "", "in_deck": cand["in_deck"], "keep_first": bool(keep),
               "apply_models": ",".join(apply), "reason": (p.get("reason") or "")[:500], "day": sl.get("day"), "revert": False,
               "asked": cand["id"] in asked, "rejected_models": sorted(set(no))}
        if not cand["in_deck"]:
            if cand["id"] not in asked and asks >= MAX_ASKS:
                row.update(test="waiting", test_note="the model is not in deck: I will propose to add it next time", status="new", asked=False)
                rows.append(row)
                continue
            if cand["id"] not in asked:
                asks += 1
                kind = "text" if t["role"] in ("agent", "text") else t["role"]
                body = {"connection": cand["connection"], "model": cand["model_id"], "kind": kind}
                if t["role"] == "agent":
                    body["agent"] = "goose"
                try:
                    call("deck.model_set", body)
                except Failure as e:
                    row["test_note"] = "could not propose the model: " + str(e.details)[:150]
                asked.add(cand["id"])
                row["asked"] = True
            row.update(test="waiting", test_note=row.get("test_note") or "the model is not in deck: waits for the owner's Add, I will check it after",
                       status="new")
            rows.append(row)
            continue
        key = (cand["id"], t["role"], need_bytes(t["id"], t["role"]))
        if key not in tests:
            m = deck.get(cand["id"])
            why_not = fit(cand["id"], cand["connection"], t["id"], t["role"])
            if why_not:
                tests[key] = (False, 0, why_not)  # no call: the model would refuse the real work
            elif len(tests) >= MAX_TESTS:
                tests[key] = (None, 0, "I will check it next time")
            else:
                try:
                    tests[key] = run_test(m, t["role"], t["id"]) if m else (False, 0, "the model is not in deck")
                except Failure as e:
                    why = {"403": "no permission: " + str(e.details)[:200],
                           "413": "413: the request is over the free tier's tokens a minute: the work does not fit",
                           "429": "429: the free tier's request limit is used up"}.get(e.error) \
                        or f"error {e.error}: {str(e.details)[:120]}"
                    tests[key] = (None if e.error == "403" else False, 0, why)
                except TimeoutError:
                    tests[key] = (False, 0, "did not answer in 150 s (TimeoutError: timed out)")
                except Exception as e:
                    tests[key] = (False, 0, f"{type(e).__name__}: {e}"[:150])
        ok, sec, note = tests[key]
        # a failed test is not shown: the model cools down for this work (trials) and the row goes stale
        row.update(test="ok" if ok else ("fail" if ok is False else "waiting"), latency=round(sec, 1) if sec else None,
                   test_note=note[:300], status="ready" if ok else ("stale" if ok is False else "new"))
        if ok is False:
            prev_t = trials.get((t["id"], cand["id"])) or {}
            fails = (prev_t.get("fails") or 0) + 1
            trials[(t["id"], cand["id"])] = {
                "target": t["id"], "model": cand["id"], "day": today.isoformat(), "fails": fails, "note": note[:300], "whole": whole(note),
                "until": (today + datetime.timedelta(days=COOLDOWN[min(fails, len(COOLDOWN)) - 1])).isoformat(), "new": True}
        rows.append({k: v for k, v in row.items() if v is not None})
    rows = [dict(old.get(r["target"]) or {}, **r) for r in rows]  # keep the work row: the model now, its health, the watch
    if rows:
        call("deck.record_upsert", {"collection": "proposals", "records": "\n".join(json.dumps(x, ensure_ascii=False) for x in rows)})
    fresh = [{k: v for k, v in x.items() if k != "new"} for x in trials.values() if x.get("new")]
    if fresh:
        call("deck.record_upsert", {"collection": "trials", "records": "\n".join(json.dumps(x, ensure_ascii=False) for x in fresh)})
    ready = sum(1 for r in rows if r["status"] == "ready")
    if ready:
        call("deck.owner_notify", {"text": f"Picked models: {ready} ready to apply.", "screen": "proposals"})
    return {"ready": ready, "waiting": sum(1 for r in rows if r.get("test") == "waiting"),
            "failed": sum(1 for r in rows if r.get("test") == "fail"), "refused": refused,
            "tests": [f"{k[0]} {k[1]}: {'ok' if v[0] else ('fail' if v[0] is False else 'wait')} {v[2][:80]}" for k, v in tests.items()]}


run(main)
