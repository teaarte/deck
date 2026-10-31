#!/usr/bin/env python3
"""Compares the models of deck's work with the list: notes models in use that are gone or broken, marks proposals
the owner applied or that went stale, and gives the plan agent every piece of work with the free or cheaper models
that could do it."""
import datetime, json, os, re, sys, traceback, urllib.error, urllib.request

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


ROLES = {"agent", "text", "image", "tts", "stt", "music", "video"}
CLI = {"claude-code", "codex"}
# Work the owner keeps on Claude (responsible steps and those that need a local browser): only a broken
# fallback of their chain is replaced, the first model stays.
KEEP_FIRST = {"master.*"}
# kinds of the list that do the work of a role
KINDS = {"agent": {"llm", "other"}, "text": {"llm", "other"}, "image": {"image"}, "tts": {"tts"}, "stt": {"stt"},
         "music": {"music"}, "video": {"video"}}
MAX_PRICE_IN = 0.5  # USD per 1M input tokens: what counts as cheap for a text model
OPEN = ("new", "ready", "asked")
COOLDOWN = (3, 7, 14)  # days a model is not proposed again for a work after its 1st, 2nd, later failed test


def unhealthy(m):
    """Red in deck's health: it errs, or fails deck's self-check because of the model itself."""
    return m.get("status") == "error" or (m.get("status") == "warning" and m.get("verdict") == "model")


def load_limits():
    """limits.json of the module: free-tier limits of models and the size of each piece of work."""
    try:
        return json.loads((call("deck.document_get", {"project": "free-models", "name": "limits.json", "fields": ["text"]}) or {}).get("text") or "{}")
    except (Failure, ValueError):
        return {}


def need_tokens(lim, target, role):
    """Tokens one request of this work carries: what one run reads, with the prompt and the tools."""
    kind = "agent" if role == "agent" else "text"
    size = (lim.get("work") or {}).get(target) or (lim.get("work_default") or {}).get(kind) or (60000 if kind == "agent" else 8000)
    return int(size / (lim.get("bytes_per_token") or 3)) + int((lim.get("overhead_tokens") or {}).get(kind) or 0)


def fit(lim, name, conn, context, target, role):
    """None when the work fits the limits of the model, else why it does not."""
    if role not in ("agent", "text"):
        return None
    l = dict((lim.get("connections") or {}).get(conn) or {}, **((lim.get("models") or {}).get(name) or {}))
    need = need_tokens(lim, target, role)
    tpm, ctx = l.get("tpm"), l.get("context") or context
    if tpm and need > tpm:
        return f"работа ~{need // 1000} тыс. токенов за запрос, а лимит {tpm} токенов в минуту ({l.get('why') or conn})"
    if ctx and need > ctx * 0.8:
        return f"работа ~{need // 1000} тыс. токенов, а контекст модели {ctx // 1000} тыс."
    return None


def whole(note):
    """A failure of the model itself (hangs, 5xx, gone), not of this work: it cools down for every work."""
    return bool(re.search(r"timed out|Timeout|не ответил|ошибка (5\d\d|404)", note or ""))


def keep_first(project, command):
    return f"{project}.{command}" in KEEP_FIRST or f"{project}.*" in KEEP_FIRST


def cost(m):
    """What a model costs, as a number to compare: 0 free or local, the price of 1M in+out tokens, a call or 1M
    characters; a subscription counts high, since the owner spares it for their own work."""
    if m.get("free") or m.get("local"):
        return 0.0
    if m.get("subscription"):
        return 5.0
    if m.get("call_usd"):
        return float(m["call_usd"]) * 10
    if m.get("input_usd") is not None or m.get("output_usd") is not None:
        return float(m.get("input_usd") or 0) + float(m.get("output_usd") or 0)
    return 3.0  # no price known


def price_text(m):
    if m.get("free") or m.get("local"):
        return "free"
    if m.get("subscription"):
        return "subscription"
    return m.get("price") or "?"


def write_slice(data):
    path = os.path.join(os.environ["DECK_DATA"], "slice.json")
    tmp = f"{path}.{os.getpid()}.tmp"
    with open(tmp, "w") as f:
        json.dump(data, f, ensure_ascii=False)
    os.replace(tmp, path)


def main(inp):
    got = call("deck.models_list") or {}
    local = {c["name"] for c in got.get("connections") or [] if c.get("local")}
    conns = {c["name"] for c in got.get("connections") or []}
    deck = {}
    for m in got.get("models") or []:
        m = dict(m, local=m.get("connection") in local)
        deck[m["name"]] = m
    lim = load_limits()
    recs = call("deck.records_list", {"collection": "models", "fields": [
        "source", "model_id", "name", "kind", "status", "score", "tier", "price_in", "price_out", "tools", "context"]}) or []
    rec = {(r["data"].get("source"), r["data"].get("model_id")): r["data"] for r in recs}

    def rec_of(name):
        m = deck.get(name) or {}
        return rec.get((m.get("connection"), m.get("model"))) or {}

    # the work of every project by command (reads models of "*"); project names come from the list kept between runs
    # and the input, and a project deck does not let it read keeps its rows
    path = os.path.join(os.environ["DECK_DATA"], "work.json")
    kept = []
    if os.path.exists(path):
        with open(path) as f:
            kept = json.load(f)
    given = inp.get("work")
    if isinstance(given, str) and given.strip():
        given = json.loads(given)
    if isinstance(given, dict):  # the whole answer of deck.models_list: its work, or its assignments and uses
        given = given.get("work") or [dict(a, model=a.get("using") or a.get("model")) for a in
                                      (given.get("assignments") or []) + (given.get("uses") or [])]
    given = given or []
    names = {r.get("project") for r in kept + given} | {p.strip() for p in (inp.get("projects") or "").split(",")}
    rows, read = [], set()
    for p in sorted(n for n in names if n):
        try:
            rows += (call("deck.models_list", {"project": p, "fields": ["work"]}) or {}).get("work") or []
            read.add(p)
        except Failure:
            pass
    rows += [r for r in given if r.get("project") not in read]
    rows += [r for r in kept if r.get("project") not in read | {g.get("project") for g in given}]
    if rows:
        tmp = f"{path}.{os.getpid()}.tmp"
        with open(tmp, "w") as f:
            json.dump(rows, f, ensure_ascii=False)
        os.replace(tmp, path)
    targets = []
    for row in rows:
        p = row.get("project")
        if not p or row.get("role") not in ROLES:
            continue
        chain = row.get("models") or ([row["model"]] if row.get("model") else [])
        if any(t["id"] == f"{p}/{row.get('command') or '-'}/{row['role']}" for t in targets):
            continue
        targets.append({
            "id": f"{p}/{row.get('command') or '-'}/{row['role']}", "project": p, "command": row.get("command") or "",
            "title": row.get("title") or row.get("command") or row["role"], "role": row["role"],
            "using": row.get("model") or "", "chain": chain, "price": row.get("price") or price_text(deck.get(row.get("model"), {})),
            "choices": row.get("choices") or [],
        })
    told = os.path.join(os.environ["DECK_DATA"], "no-work-told.txt")
    if not targets:
        # nothing to compare: the owner is told once what to give, and the chain stops
        if not any(names):
            why = ("«Сравнить с deck» не знает, какие модули смотреть: запустите его и перечислите модули в поле "
                   "projects через запятую — дальше оно их запомнит.")
        elif not read:
            why = ("«Сравнить с deck» не может прочитать, какие модели делают работу модулей: выдайте free-models "
                   "право reads models в Настройки → Модули.")
        else:
            why = "«Сравнить с deck»: у названных модулей нет работы для моделей."
        if not os.path.exists(told) or open(told).read() != why:
            call("deck.notify", {"text": why})
            with open(told, "w") as f:
                f.write(why)
        return None
    if os.path.exists(told):
        os.remove(told)

    # (a) models in use that are gone at their source or broken in deck
    problems = {}
    for t in targets:
        for name in t["chain"]:
            m, r = deck.get(name) or {}, rec_of(name)
            if name not in deck:
                problems[name] = "модели нет в deck"
            elif r.get("status") == "gone":
                problems[name] = "пропала у источника"
            elif unhealthy(m):
                problems[name] = "не проходит самопроверку deck" if m.get("status") == "warning" else "не отвечает в deck"
    old_alerts = {r["data"]["model"]: r for r in call("deck.records_list", {"collection": "alerts"}) or []}
    day = datetime.date.today().isoformat()
    yesterday = (datetime.date.today() - datetime.timedelta(days=1)).isoformat()
    alert_rows, fresh = [], []
    for name, why in problems.items():
        a = old_alerts.get(name)
        if not a or not a["data"].get("active"):
            fresh.append(f"{name} ({why})")
        if not a or not a["data"].get("active") or a["data"].get("reason") != why:
            alert_rows.append({"model": name, "reason": why, "day": day, "active": True})
    for name, a in old_alerts.items():
        if name not in problems and a["data"].get("active"):
            alert_rows.append(dict(a["data"], active=False))
    if alert_rows:
        call("deck.records_upsert", {"collection": "alerts", "records": "\n".join(json.dumps(x, ensure_ascii=False) for x in alert_rows)})
    if fresh:
        call("deck.notify", {"text": "Модели в работе сломались: " + ", ".join(fresh) + ". Подберу замену — смотрите «Предложения».",
                             "screen": "proposals"})

    # proposals made before: applied, stale, or still open
    props = {r["data"]["target"]: r for r in call("deck.records_list", {"collection": "proposals"}) or []}
    by_id = {t["id"]: t for t in targets}

    # failed tests: the model cools down for that work (for every work when the model itself failed)
    today = datetime.date.today()
    trials = {(x["data"]["target"], x["data"]["model"]): x["data"] for x in call("deck.records_list", {"collection": "trials"}) or []}
    seed = []
    for tid, r in props.items():
        d = r["data"]
        k = (tid, d.get("proposed"))
        if d.get("test") == "fail" and d.get("proposed") and d.get("day") and (trials.get(k) or {}).get("day", "") < d["day"]:
            trials[k] = {"target": tid, "model": d["proposed"], "day": d["day"], "fails": 1, "note": (d.get("test_note") or "")[:300],
                         "whole": whole(d.get("test_note")),
                         "until": (datetime.date.fromisoformat(d["day"]) + datetime.timedelta(days=COOLDOWN[0])).isoformat()}
            seed.append(trials[k])
    if seed:
        call("deck.records_upsert", {"collection": "trials", "records": "\n".join(json.dumps(x, ensure_ascii=False) for x in seed)})

    def cooling(tid, model):
        return any(tr.get("until", "") >= day and (tt == tid or tr.get("whole")) for (tt, mm), tr in trials.items() if mm == model)

    def chain_of(d):
        return [m for m in (d.get("apply_models") or "").split(",") if m]

    changed, open_ok, rejected = [], set(), {}
    for tid, r in props.items():
        d, t = r["data"], by_id.get(tid)
        rejected.setdefault(tid, []).extend(d.get("rejected_models") or [])
        if d.get("status") == "rejected":
            rejected.setdefault(tid, []).append(d.get("proposed"))
            continue
        if d.get("status") not in OPEN:
            continue
        if t and (t["chain"] == chain_of(d) if d.get("revert") else
                  d.get("proposed") in t["chain"] and (t["chain"][0] == d["proposed"] or d.get("keep_first"))):
            # applied: watch blames or clears the change by the real runs of the next days (not a revert)
            changed.append(dict(d, status="applied", watch_from=day, watch_prev=d.get("current_models") or [],
                                watch_done=bool(d.get("revert")), watch_verdict="", watch_note=""))
        elif not t or t["chain"] != d.get("current_models"):
            changed.append(dict(d, status="stale"))
        elif d.get("proposed") in problems or unhealthy(deck.get(d.get("proposed")) or {}) or d.get("test") == "fail" \
                or (not d.get("revert") and cooling(tid, d.get("proposed"))):
            changed.append(dict(d, status="stale"))  # broken or failed its test: the trial keeps it from coming back soon
        else:
            open_ok.add(tid)
            if d.get("status") == "asked" and (d.get("ask_day") or "") < yesterday:
                changed.append(dict(d, status="ready", test="ok"))  # the owner said no, or the ask expired: the button is back
    if changed:
        call("deck.records_upsert", {"collection": "proposals", "records": "\n".join(json.dumps(x, ensure_ascii=False) for x in changed)})
    latest = {tid: dict(r["data"]) for tid, r in props.items()}
    latest.update({c["target"]: c for c in changed})

    # models of each chain whose limits the work exceeds count as broken for that work
    unfit = {t["id"]: {n: w for n in t["chain"] if (w := fit(lim, n, (deck.get(n) or {}).get("connection"), rec_of(n).get("context"),
                                                          t["id"], t["role"]))} for t in targets}

    # one row per piece of work for the screen: the model now, its health and what the owner should know
    rows_out = []
    for t in targets:
        old = latest.get(t["id"]) or {"target": t["id"], "project": t["project"], "command": t["command"], "role": t["role"], "status": "none"}
        why = {n: problems.get(n) or unfit[t["id"]].get(n) for n in t["chain"] if n in problems or n in unfit[t["id"]]}
        now_bad, fb_bad = why.get(t["using"]), [n for n in why if n != t["using"]]
        alert = (f"{t['using']}: {now_bad}" if now_bad else
                 "запасная " + ", ".join(f"{n}: {why[n]}" for n in fb_bad) if fb_bad else "")
        if old.get("status") == "applied" and not old.get("watch_done"):
            alert = alert or old.get("alert") or "наблюдаю после замены"
        new = dict(old, title=t["title"], now=t["using"], now_models=t["chain"], health="error" if now_bad else ("warn" if fb_bad else "ok"),
                   alert=alert[:300])
        # the tag of the row: the test of an open proposal, the ask, or the watch after a change
        st = new.get("status")
        if st == "asked":
            new["test"] = "asked"
        elif st == "applied":
            new["test"] = None if new.get("watch_done") else "watching"
        elif st not in OPEN:
            new["test"] = None
        new = {k: v for k, v in new.items() if v is not None}
        if new != old:
            rows_out.append(new)
    if rows_out:
        call("deck.records_upsert", {"collection": "proposals", "records": "\n".join(json.dumps(x, ensure_ascii=False) for x in rows_out)})

    # (b) for each piece of work: the free or cheaper models that could do it
    plan = []
    for t in targets:
        if t["id"] in open_ok:
            continue
        keep = keep_first(t["project"], t["command"])
        broken = [n for n in t["chain"] if n in problems or n in unfit[t["id"]]]
        if keep and not broken:
            continue
        cur = deck.get(t["using"]) or {}
        cur_cost, cur_score = cost(cur), rec_of(t["using"]).get("score") or 0
        free_agents = [c for c in t["choices"] if c not in CLI]
        if t["role"] == "agent" and not free_agents:
            continue  # only Claude Code or Codex can run it (a local browser, say)
        cands = []
        pool = t["choices"] if t["choices"] else [n for n, m in deck.items() if m.get("kind") == ("text" if t["role"] in ("agent", "text") else t["role"])]
        for n in pool:
            m = deck.get(n)
            if not m or n in CLI or n == t["using"] or n in problems or unhealthy(m) or cooling(t["id"], n):
                continue
            if fit(lim, n, m.get("connection"), rec_of(n).get("context"), t["id"], t["role"]):
                continue
            if t["role"] == "agent" and not m.get("agent"):
                continue
            r = rec_of(n)
            cands.append({"id": n, "in_deck": True, "connection": m.get("connection"), "model_id": m.get("model"),
                          "price": price_text(m), "cost": round(cost(m), 3), "score": r.get("score"),
                          "status": m.get("status") or "unchecked", "speed": round(m.get("speed") or 0) or None,
                          "note": (m.get("error") or "")[:160]})
        in_deck = {(m.get("connection"), m.get("model")) for m in deck.values()}
        for (src, mid), r in rec.items():
            if r.get("status") != "rated" or (r.get("score") or 0) < 7 or (src, mid) in in_deck or src not in conns or src in local:
                continue
            if r.get("kind") not in KINDS[t["role"]] or re.search(r"anthropic|claude", mid):
                continue  # the owner keeps Claude for their own work
            if t["role"] in ("agent", "text") and r.get("kind") == "other" and not r.get("tools"):
                continue
            if t["role"] == "agent" and r.get("tools") is False and src != "nvidia":
                continue
            tier = r.get("tier") or "free"
            if tier == "cheap" and (r.get("price_in") or 0) > MAX_PRICE_IN:
                continue
            if cooling(t["id"], f"{src}:{mid}") or fit(lim, f"{src}:{mid}", src, r.get("context"), t["id"], t["role"]):
                continue
            c = 0.0 if tier == "free" else float(r.get("price_in") or 0) + float(r.get("price_out") or 0)
            cands.append({"id": f"{src}:{mid}", "in_deck": False, "connection": src, "model_id": mid,
                          "price": "free" if tier == "free" else f"${r.get('price_in')} / ${r.get('price_out')} per 1M",
                          "cost": round(c, 3), "score": r.get("score"), "status": "not in deck", "name": r.get("name")})
        cands = [c for c in cands if c["id"] not in (rejected.get(t["id"]) or [])]
        # better: cheaper, or as cheap and stronger than a model whose strength is known (a local one stays)
        better = [c for c in cands if c["cost"] < cur_cost or (c["cost"] <= cur_cost and cur_score and (c["score"] or 0) > cur_score)]
        if not broken and not better:
            continue
        pool_c = better if not broken else cands
        pool_c.sort(key=lambda c: (c["status"] not in ("ok",), c["cost"], -(c["score"] or 0)))
        plan.append(dict({k: t[k] for k in ("id", "project", "command", "title", "role", "using", "chain", "price")},
                         using_cost=round(cur_cost, 3), using_score=cur_score or None, broken=broken, keep_first=keep,
                         broken_why={n: problems.get(n) or unfit[t["id"]].get(n) for n in broken},
                         rejected=rejected.get(t["id"]) or [], candidates=pool_c[:6]))

    out = {"day": day, "work_rows": len(rows_out), "targets": plan, "alerts": sorted(problems.items()), "applied": sum(1 for c in changed if c["status"] == "applied"),
           "stale": sum(1 for c in changed if c["status"] == "stale"), "read": sorted(read),
           "kept": sorted({r.get("project") for r in rows if r.get("project") not in read})}
    write_slice(out)
    if not plan:
        return None  # nothing to plan: the chain stops here
    return out


run(main)
