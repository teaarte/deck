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

// locked runs fn holding the lock of file for a read-modify-write of it. Node has no flock, so the lock is a
// .lock file made exclusively beside it; one left by a run that died is taken after two minutes.
export async function locked(file, fn) {
  const lock = file + ".lock";
  for (let i = 0; ; i++) {
    try {
      fs.closeSync(fs.openSync(lock, "wx"));
      break;
    } catch (e) {
      if (e.code !== "EEXIST") throw e;
      try {
        if (Date.now() - fs.statSync(lock).mtimeMs > 120_000) fs.rmSync(lock, { force: true });
      } catch {}
      await new Promise((r) => setTimeout(r, Math.min(50 * (i + 1), 500)));
    }
  }
  try {
    return await fn();
  } finally {
    fs.rmSync(lock, { force: true });
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
      : { error: String(e).slice(0, 300), details: String(e.stack || e).slice(-4000) };
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
