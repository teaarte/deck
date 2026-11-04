import { signedOut } from './signin';
import { createStore, useStore } from './store';

// LiveEvent says a topic changed, in a project when it names one.
export interface LiveEvent { topic: string; project?: string }

type Listener = { event: (e: LiveEvent) => void; resume?: () => void };
const listeners = new Set<Listener>();
let source: { close: () => void } | undefined;
let retry: ReturnType<typeof setTimeout> | undefined;
// lost is set while a live channel that was open is down; screens drawn meanwhile may be old.
const lost = createStore(false);
const setLost = lost.set;
export const useLive = () => !useStore(lost);

// resume tells the listeners once that the page may have missed events: it was hidden, or the channel was down.
let resuming: ReturnType<typeof setTimeout> | undefined;
function resume() {
  if (resuming) return;
  resuming = setTimeout(() => {
    resuming = undefined;
    listeners.forEach((l) => l.resume?.());
  }, 250);
}
document.addEventListener('visibilitychange', () => { if (!document.hidden && listeners.size) resume(); });

// SILENT is how long a live channel may say nothing, not even its ping every 15 s, before it is taken for dead.
const SILENT = 40_000;
let watchdog: ReturnType<typeof setTimeout> | undefined;

// Channel is the stream of events as the page reads it: it says when it opens, each event's data, each ping, and
// that it failed, closed for good or trying again by itself.
interface Channel { close: () => void }
interface Heard { open: () => void; data: (d: string) => void; ping: () => void; fail: (closed: boolean) => void }

// byCookie is the browser's own reader of the stream, which sends the cookie and tries again by itself.
function byCookie(h: Heard): Channel {
  const es = new EventSource('/api/events');
  es.onopen = h.open;
  es.addEventListener('ping', h.ping);
  es.onmessage = (m) => h.data(String(m.data));
  es.onerror = () => h.fail(es.readyState === EventSource.CLOSED);
  return { close: () => es.close() };
}

// openSource keeps one live channel for every screen of the page and opens it again when it closes or falls silent;
// a channel it replaced says nothing more.
function openSource() {
  clearTimeout(retry);
  source?.close();
  let ch: Channel | undefined;
  const mine = () => ch !== undefined && ch === source;
  const heard = () => {
    clearTimeout(watchdog);
    watchdog = setTimeout(() => {
      if (!mine()) return;
      setLost(true);
      openSource();
    }, SILENT);
  };
  ch = byCookie({
    open: () => {
      if (!mine()) return;
      heard();
      if (lost.get()) resume();
      setLost(false);
    },
    ping: () => { if (mine()) heard(); },
    data: (d) => {
      if (!mine()) return;
      heard();
      if (!d) return;
      const e = JSON.parse(d) as LiveEvent;
      listeners.forEach((l) => l.event(e));
    },
    fail: (closed) => {
      if (!mine()) return;
      setLost(true);
      if (!closed) return;
      if (listeners.size && !signedOut()) retry = setTimeout(openSource, 3000);
      else source = undefined;
    },
  });
  source = ch;
  heard();
}

// events follows the live channel of the page; the last one to leave closes it.
export function events(onEvent: (e: LiveEvent) => void, onResume?: () => void): () => void {
  const l: Listener = { event: onEvent, resume: onResume };
  listeners.add(l);
  if (!source) openSource();
  return () => {
    listeners.delete(l);
    if (listeners.size) return;
    clearTimeout(retry);
    clearTimeout(watchdog);
    source?.close();
    source = undefined;
    setLost(false);
  };
}
