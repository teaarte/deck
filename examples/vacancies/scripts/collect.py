"""Collect new job postings from every source in the `sources` collection.

Input (stdin JSON): {"source": "<url>"} optional, to read only one source.
Result (stdout JSON): {"new": n, "waiting": n, "ids": [...], "sources": [...]} or null when nothing
is new, nothing waits for a rating and no source failed.

Each source keeps a cursor in its record, so after the machine slept the next run takes
everything since the cursor. A source that cannot be read gets status "error" with the
last error; the other sources go on.
"""
import datetime
import html
import json
import os
import re
import sys
import urllib.request

DEFAULT_SOURCES = [
    {
        "url": "https://hn.algolia.com/api/v1/search_by_date?tags=story&query=%22who%20is%20hiring%22",
        "name": "HN Who is hiring",
        "parser": "hn_whoishiring",
    },
    {
        "url": "https://remoteok.com/api",
        "name": "Remote OK",
        "parser": "remoteok",
    },
]

UA = "Mozilla/5.0 (deck vacancies collector)"
TEXT_LIMIT = 6000


def log(*args):
    print(*args, file=sys.stderr, flush=True)


# ---------- deck ----------

def deck(command, payload):
    url = os.environ["DECK_URL"].rstrip("/") + "/commands/deck." + command
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode(),
        headers={
            "Authorization": "Bearer " + os.environ["DECK_TOKEN"],
            "Content-Type": "application/json",
        },
    )
    body = None
    errors = []
    # deck is local; try without the web proxy first, then through it.
    for opener in (urllib.request.build_opener(urllib.request.ProxyHandler({})),
                   urllib.request.build_opener()):
        try:
            with opener.open(req, timeout=60) as r:
                body = r.read().decode()
            break
        except Exception as e:  # noqa: BLE001
            errors.append(repr(e))
    if body is None:
        raise RuntimeError("deck.%s failed: %s" % (command, "; ".join(errors)))
    data = json.loads(body) if body.strip() else None
    if isinstance(data, dict) and "result" in data and len(data) <= 2:
        data = data["result"]
    return data


def list_records(collection):
    data = deck("records_list", {"collection": collection})
    if isinstance(data, dict):
        for k in ("records", "items", "data"):
            if isinstance(data.get(k), list):
                data = data[k]
                break
    out = []
    for item in data or []:
        rec = item.get("data", item) if isinstance(item, dict) else None
        if isinstance(rec, str):
            rec = json.loads(rec)
        if isinstance(rec, dict):
            out.append(rec)
    return out


def upsert(collection, records):
    if not records:
        return []
    jsonl = "\n".join(json.dumps(r, ensure_ascii=False) for r in records)
    data = deck("records_upsert", {"collection": collection, "records": jsonl})
    if isinstance(data, dict):
        for k in ("ids", "records"):
            if isinstance(data.get(k), list):
                return data[k]
    return data if isinstance(data, list) else []


# ---------- web ----------

def get_json(url):
    req = urllib.request.Request(url, headers={"User-Agent": UA, "Accept": "application/json"})
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.loads(r.read().decode())


def fix_mojibake(s):
    """Remote OK sends UTF-8 that was decoded as Latin-1 once more ("Weâre")."""
    if not s or not re.search("[ÂÃâ][\x80-\xbf]", s):
        return s
    for enc in ("latin-1", "cp1252"):
        try:
            return s.encode(enc).decode("utf-8")
        except (UnicodeEncodeError, UnicodeDecodeError):
            continue
    return s


def clean(s, limit=200):
    return fix_mojibake(s or "").strip().strip(",").strip()[:limit]


def html_to_text(s):
    s = fix_mojibake(s or "")
    s = re.sub(r"(?i)<br\s*/?>", "\n", s)
    s = re.sub(r"(?i)</?p[^>]*>", "\n\n", s)
    s = re.sub(r"(?i)</li>", "\n", s)
    s = re.sub(r"<[^>]+>", "", s)
    s = html.unescape(s)
    s = re.sub(r"\n{3,}", "\n\n", s)
    return s.strip()


SALARY_RE = re.compile(r"([$€£]\s?\d[\d,.]*\s?[kK]?(\s?[-–]\s?[$€£]?\s?\d[\d,.]*\s?[kK]?)?|\d{2,3}\s?[kK]\s?[-–]\s?\d{2,3}\s?[kK])")
ROLE_WORDS = ("engineer", "developer", "scientist", "designer", "manager", "lead", "architect",
              "analyst", "devops", "sre", "researcher", "cto", "head of", "director", "full-stack",
              "fullstack", "backend", "frontend", "product", "founding")
REMOTE_WORDS = ("remote",)


def guess_fields(text):
    """Best-effort fields from the HN convention 'Company | Role | Location | Remote | Salary'."""
    first = text.split("\n", 1)[0]
    parts = [p.strip() for p in re.split(r"\s[|•·]\s|\s[-–]\s(?=[A-Z])", first) if p.strip()]
    company = parts[0] if parts else ""
    role = location = ""
    for p in parts[1:]:
        low = p.lower()
        if not role and any(w in low for w in ROLE_WORDS):
            role = p
        elif not location and not SALARY_RE.search(p) and low not in ("remote", "full-time", "full time",
                                                                          "part-time", "contract", "onsite"):
            location = p
    if not role and len(parts) > 1:
        role = parts[1]
    low = first.lower()
    remote = any(w in low for w in REMOTE_WORDS)
    m = SALARY_RE.search(first)
    return {
        "company": clean(company),
        "role": clean(role),
        "location": clean(location),
        "remote": remote,
        "salary": m.group(0).strip() if m else "",
    }


# ---------- parsers ----------
# A parser gets (source record, cursor) and returns (postings, new cursor).

def parse_hn_whoishiring(source, cursor):
    search = get_json(source["url"])
    threads = [h for h in search.get("hits", [])
               if h.get("author") == "whoishiring"
               and (h.get("title") or "").lower().startswith("ask hn: who is hiring")]
    if not threads:
        raise RuntimeError("no 'Ask HN: Who is hiring?' thread in the search result")
    thread = max(threads, key=lambda h: h.get("created_at_i", 0))
    thread_id = int(thread["objectID"])
    since = 0
    if cursor.get("thread_id") == thread_id:
        since = int(cursor.get("max_comment_id") or 0)
    item = get_json("https://hn.algolia.com/api/v1/items/%d" % thread_id)
    postings = []
    max_id = since
    for c in item.get("children") or []:
        cid = int(c.get("id") or 0)
        max_id = max(max_id, cid)
        if cid <= since or not c.get("text"):
            continue
        text = html_to_text(c["text"])
        p = guess_fields(text)
        p.update({
            "url": "https://news.ycombinator.com/item?id=%d" % cid,
            "text": text[:TEXT_LIMIT],
            "posted": (c.get("created_at") or "")[:10],
        })
        postings.append(p)
    return postings, {"thread_id": thread_id, "thread_title": thread.get("title"), "max_comment_id": max_id}


def parse_remoteok(source, cursor):
    data = get_json(source["url"])
    since = int(cursor.get("max_epoch") or 0)
    postings = []
    max_epoch = since
    for j in data:
        if not isinstance(j, dict) or not j.get("id"):
            continue  # the first element is the legal notice
        epoch = int(j.get("epoch") or 0)
        max_epoch = max(max_epoch, epoch)
        if epoch <= since:
            continue
        smin, smax = j.get("salary_min") or 0, j.get("salary_max") or 0
        salary = ""
        if smin or smax:
            salary = "$%s–$%s" % (smin or "?", smax or "?")
        text = html_to_text(j.get("description"))
        tags = ", ".join(j.get("tags") or [])
        if tags:
            text = "Tags: %s\n\n%s" % (tags, text)
        postings.append({
            "url": j.get("url") or j.get("apply_url"),
            "company": clean(j.get("company")),
            "role": clean(j.get("position")),
            "location": clean(j.get("location")),
            "remote": True,
            "salary": salary,
            "text": text[:TEXT_LIMIT],
            "posted": (j.get("date") or "")[:10],
        })
    return postings, {"max_epoch": max_epoch}


PARSERS = {
    "hn_whoishiring": parse_hn_whoishiring,
    "remoteok": parse_remoteok,
}


def pick_parser(source):
    name = source.get("parser") or ""
    if not name:
        url = source.get("url", "")
        if "hn.algolia.com" in url:
            name = "hn_whoishiring"
        elif "remoteok.com" in url:
            name = "remoteok"
    return name, PARSERS.get(name)


# ---------- main ----------

def main():
    raw = sys.stdin.read().strip()
    inp = json.loads(raw) if raw else {}
    only = (inp or {}).get("source")

    sources = list_records("sources")
    if not sources:
        log("sources collection is empty; seeding the default sources")
        sources = [dict(s, status="new", last_error="", cursor={}) for s in DEFAULT_SOURCES]
        upsert("sources", sources)

    # An upsert replaces the whole record: never write over a posting that is already rated.
    rated = {p.get("url") for p in list_records("postings") if p.get("rating")}

    now = datetime.datetime.now().astimezone()
    today = now.date().isoformat()
    all_ids = []
    report = []
    for src in sources:
        if only and src.get("url") != only:
            continue
        if src.get("enabled") is False:
            continue
        name, parser = pick_parser(src)
        entry = {"name": src.get("name") or src.get("url"), "url": src.get("url")}
        try:
            if not parser:
                raise RuntimeError("no parser %r for this source" % name)
            postings, cursor = parser(src, src.get("cursor") or {})
            postings = [p for p in postings if p.get("url") and p["url"] not in rated]
            for p in postings:
                p["source"] = src["url"]
                p["source_name"] = src.get("name") or ""
                p["found"] = today
            ids = upsert("postings", postings)
            all_ids.extend(ids)
            src.update(status="ok", last_error="", last_read=now.isoformat(timespec="seconds"),
                       last_new=len(postings), cursor=cursor)
            entry.update(status="ok", new=len(postings))
            log("%s: %d new" % (entry["name"], len(postings)))
        except Exception as e:  # noqa: BLE001
            err = "%s: %s" % (type(e).__name__, e)
            src.update(status="error", last_error=err[:500], last_read=now.isoformat(timespec="seconds"))
            entry.update(status="error", new=0, error=err[:500])
            log("%s: ERROR %s" % (entry["name"], err))
        upsert("sources", [src])
        report.append(entry)

    total = sum(e.get("new", 0) for e in report)
    # Postings left over from earlier days (a batch is limited) keep the rating chain going.
    waiting = sum(1 for p in list_records("postings") if not p.get("rating"))
    if total == 0 and waiting == 0 and not any(e["status"] == "error" for e in report):
        print("null")
        return
    print(json.dumps({"new": total, "waiting": waiting, "ids": all_ids, "sources": report},
                     ensure_ascii=False))


if __name__ == "__main__":
    main()
