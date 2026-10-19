// Helper for a Node script of a module (.mjs): copy the part above "Example" into the script (a script runs alone,
// it cannot import this file) and keep its names.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// Failure is a failure of the run: error is one line for the owner, type one deck takes (none lets deck type it).
export class Failure extends Error {
  constructor(error, type, details) {
    super(error);
    this.type = type;
    this.details = details;
  }
}

// call calls a deck command and returns its result; an error answer throws Failure with deck's answer as details.
// fetch gives up on an answer after 5 minutes: a longer command is started and then read with deck.process_get.
export async function call(command, input = {}) {
  const res = await fetch(`${process.env.DECK_URL}/commands/${command}`, {
    method: "POST",
    headers: { Authorization: `Bearer ${process.env.DECK_TOKEN}`, "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  const body = await res.text();
  // No type: deck types a failure by its own last error answer.
  if (!res.ok) throw new Failure(`${command}: deck answered ${res.status}`, undefined, body.slice(0, 4000));
  return body.trim() ? JSON.parse(body) : null;
}

// locked runs fn holding file.lock, made exclusively and kept fresh (Node has no flock); one a dead run left is taken
// after a minute. The Python helper's flock does not see it: one file is written by scripts of one language.
export async function locked(file, fn) {
  const lock = file + ".lock";
  const mine = `${process.pid}.${Math.random()}`;
  for (let i = 0; ; i++) {
    try {
      fs.writeFileSync(lock, mine, { flag: "wx" });
      break;
    } catch (e) {
      if (e.code !== "EEXIST") throw e;
      if (breakStale(lock)) continue;
      await new Promise((r) => setTimeout(r, Math.min(50 * (i + 1), 500)));
    }
  }
  const fresh = setInterval(() => {
    try {
      fs.utimesSync(lock, new Date(), new Date());
    } catch {}
  }, 15_000);
  try {
    return await fn();
  } finally {
    clearInterval(fresh);
    try {
      if (fs.readFileSync(lock, "utf8") === mine) fs.rmSync(lock, { force: true });
    } catch {}
  }
}

// breakStale removes a lock a dead run left, one remover at a time: it looks at the lock again holding lock.break, so a
// lock another run has just taken stays.
function breakStale(lock) {
  const old = (f) => {
    try {
      return Date.now() - fs.statSync(f).mtimeMs > 60_000;
    } catch {
      return false;
    }
  };
  if (!old(lock)) return false;
  const brk = lock + ".break";
  if (old(brk)) fs.rmSync(brk, { force: true });
  try {
    fs.writeFileSync(brk, "", { flag: "wx" });
  } catch {
    return false;
  }
  try {
    if (!old(lock)) return false;
    fs.rmSync(lock, { force: true });
    return true;
  } finally {
    fs.rmSync(brk, { force: true });
  }
}

// replace writes text to file whole or not at all: a temporary file of this run beside it, then a rename. Call it
// inside locked(file).
export function replace(file, text) {
  const tmp = `${file}.${process.pid}.tmp`;
  try {
    const fd = fs.openSync(tmp, "w");
    try {
      fs.writeFileSync(fd, text);
      fs.fsyncSync(fd);
    } finally {
      fs.closeSync(fd);
    }
    fs.renameSync(tmp, file);
  } finally {
    fs.rmSync(tmp, { force: true });
  }
}

// write writes file whole under its lock.
export function write(file, text) {
  return locked(file, () => replace(file, text));
}

// run is the entry point: runs main, prints its result, and turns a failure nobody caught into the one form deck reads.
export async function run(main) {
  try {
    const input = JSON.parse(fs.readFileSync(0, "utf8") || "{}");
    console.log(JSON.stringify(await main(input)));
  } catch (e) {
    const out = e instanceof Failure ? { error: e.message, type: e.type, details: e.details }
      : { error: String(e).slice(0, 300), details: String(e?.stack ?? e).slice(-4000) };
    console.log(JSON.stringify(out));
    process.exitCode = 1;
  }
}

// Example: a step that reads a record, fails on bad input in its own words, and adds a line to a file of its data.

async function main(input) {
  if ("record" in input) await call("deck.record_get", { record: input.record });
  if (!input.value) throw new Failure("no value to add", "input.invalid", { input });
  const file = path.join(process.env.DECK_DATA, "values.json");
  const values = await locked(file, () => {
    const values = fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, "utf8")) : [];
    values.push(input.value);
    replace(file, JSON.stringify(values));
    return values;
  });
  return { values, files: fs.readdirSync(process.env.DECK_DATA).sort() };
}

if (fs.realpathSync(fileURLToPath(import.meta.url)) === fs.realpathSync(process.argv[1])) await run(main);
