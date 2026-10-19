# Helper for a Python script of a module: copy the part above "Example" into the script (a script runs alone, it
# cannot import this file) and keep its names.
import contextlib, fcntl, json, os, sys, traceback, urllib.error, urllib.request


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


@contextlib.contextmanager
def locked(path):
    """Holds the lock of path (flock of a .lock file beside it) for a read-modify-write of it; the Node helper's lock
    does not see it, so one file is written by scripts of one language."""
    with open(path + ".lock", "a") as f:
        fcntl.flock(f, fcntl.LOCK_EX)
        try:
            yield
        finally:
            fcntl.flock(f, fcntl.LOCK_UN)


def replace(path, text):
    """Writes text to path whole or not at all: a temporary file of this run beside it, then a rename. Call it
    inside locked(path)."""
    tmp = f"{path}.{os.getpid()}.tmp"
    try:
        with open(tmp, "w") as f:
            f.write(text)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, path)
    finally:
        if os.path.exists(tmp):
            os.remove(tmp)


def write(path, text):
    """Writes path whole under its lock."""
    with locked(path):
        replace(path, text)


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


# Example: a step that reads a record, fails on bad input in its own words, and adds a line to a file of its data.

def main(inp):
    if "record" in inp:
        call("deck.record_get", {"record": inp["record"]})
    if not inp.get("value"):
        raise Failure("no value to add", "input.invalid", {"input": inp})
    path = os.path.join(os.environ["DECK_DATA"], "values.json")
    with locked(path):
        values = json.load(open(path)) if os.path.exists(path) else []
        values.append(inp["value"])
        replace(path, json.dumps(values))
    return {"values": values, "files": sorted(os.listdir(os.environ["DECK_DATA"]))}


if __name__ == "__main__":
    run(main)
