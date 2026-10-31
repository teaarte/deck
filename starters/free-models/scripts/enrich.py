#!/usr/bin/env python3
"""Gives each strong model the logo of its maker (lobe-icons, cached in the data folder) and marks the ones deck
already has, for the grid of the screen."""
import json, os, re, sys, traceback, urllib.error, urllib.request

DATA = os.environ["DECK_DATA"]
ICONS = "https://unpkg.com/@lobehub/icons-static-svg@latest/icons/"
# maker of a model id prefix or a word of its name -> the lobe-icons name
MAKERS = {
    "deepseek": "deepseek", "moonshot": "moonshot", "kimi": "moonshot", "z-ai": "zhipu", "glm": "zhipu",
    "meta": "meta", "llama": "meta", "black-forest-labs": "bfl", "flux": "bfl", "xiaomi": "xiaomimimo",
    "mimo": "xiaomimimo", "anthropic": "claude", "claude": "claude", "gemini": "gemini", "gemma": "gemma",
    "google": "google", "openai": "openai", "whisper": "openai", "gpt": "openai", "qwen": "qwen", "tongyi": "qwen",
    "nvidia": "nvidia", "nemotron": "nvidia", "parakeet": "nvidia", "mistral": "mistral", "voxtral": "mistral",
    "minimax": "minimax", "stepfun": "stepfun", "stepaudio": "stepfun", "fish": "fishaudio", "ltx": "lightricks",
    "inclusionai": "ling", "ling": "ling", "thinkingmachines": "thinkingmachines", "supertone": "supertone",
}
SOURCES = {"openrouter": "openrouter", "nvidia": "nvidia", "groq": "groq", "google": "gemini",
           "cloudflare": "cloudflare", "mistral": "mistral", "pollinations": "pollinations", "stepfun": "stepfun"}
# sources that make the models they serve; the others (OpenRouter, Groq, Cloudflare…) only resell
MAKER_SOURCES = {"google", "mistral", "stepfun"}


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


def maker(m):
    mid = re.sub(r"^@cf/", "", str(m.get("model_id") or "")).lower()
    words = [mid.split("/")[0]] if "/" in mid else []
    words += re.findall(r"[a-z][a-z0-9-]*", (str(m.get("name") or "") + " " + mid).lower())
    for w in words:
        for k, v in MAKERS.items():
            if w == k or w.startswith(k):
                return v
    if "/" in mid:  # the maker named by the id, even without a logo: never the source that only resells it
        return mid.split("/")[0]
    return SOURCES.get(m.get("source")) if m.get("source") in MAKER_SOURCES else ""


def russian(s):
    """Plain Russian: Cyrillic text with at most one lowercase English word (names and terms are capitalised)."""
    if not s:
        return True
    return len(re.findall(r"[а-яё]", s, re.I)) >= 10 and len(re.findall(r"\b[a-z]{4,}\b", s)) <= 1 \
        and not re.search(r"[一-鿿぀-ヿ]", s)


def family(m):
    """One model offered by several sources: the id without the maker, the tier, the size and the dots."""
    f = re.sub(r"^@cf/", "", str(m.get("model_id") or "")).lower().split("/")[-1].split(":")[0]
    f = re.sub(r"-\d+(\.\d+)?b(-a\d+(\.\d+)?b)?(?=-|$)", "", f)  # 120b-a12b, 31b, 0.6b
    f = re.sub(r"[._]", "-", f)
    return re.sub(r"-(it|instruct|latest)$", "", f)


def logo(name):
    """The path of the maker's logo in the data folder (logos/<name>.svg), fetched from lobe-icons once;
    None when it has none."""
    if not name:
        return None
    os.makedirs(os.path.join(DATA, "logos"), exist_ok=True)
    path = os.path.join(DATA, "logos", name + ".svg")
    if not os.path.exists(path):
        svg = b""
        for v in (name + "-color", name):
            try:
                with urllib.request.urlopen(ICONS + v + ".svg", timeout=20) as r:
                    svg = r.read()
                break
            except Exception:
                continue
        tmp = f"{path}.{os.getpid()}.tmp"  # runs may overlap: each writes its own file, then swaps it in
        with open(tmp, "wb") as f:
            f.write(svg)  # an empty file remembers there is no logo
        os.replace(tmp, path)
    with open(path, "rb") as f:
        svg = f.read(256).lstrip()
    return "logos/" + name + ".svg" if svg.startswith(b"<svg") else None


def main(inp):
    try:  # deck's own models (a module reads them since deck allowed it, without keys)
        got = call("deck.models_list", {"fields": ["models.connection", "models.model"]}) or {}
        deck = {(m.get("connection"), m.get("model")) for m in got.get("models") or [] if m.get("model")}
    except Exception as e:  # keep in_deck as it was, but say so in the result
        deck, why = None, str(e)
    rows = []
    recs = call("deck.records_list", {"collection": "models", "where": "status == 'rated' && score >= 7"}) or []
    for r in sorted(recs, key=lambda r: (-(r["data"].get("score") or 0), r["id"])):
        d = r["data"]
        v = maker(d) or ""
        # only the path of the logo file; the grid reads the file itself
        new = dict(d, vendor=v, logo=(logo(v) or "") if v else "", family=family(d),
                   text_ok=russian(d.get("strengths")) and russian(d.get("limits")))
        if deck is not None:
            new["in_deck"] = (d.get("source"), d.get("model_id")) in deck
        if any(new.get(k) != d.get(k) for k in ("vendor", "logo", "in_deck", "family", "text_ok")):
            rows.append(new)
    if rows:
        call("deck.records_upsert", {"collection": "models",
                                     "records": "\n".join(json.dumps(x, ensure_ascii=False) for x in rows)})
    out = {"changed": len(rows), "in_deck": sum(1 for r in recs if (r["data"].get("source"), r["data"].get("model_id")) in (deck or ()))}
    if deck is None:
        out["deck_models"] = "not read: " + why
    return out


run(main)
