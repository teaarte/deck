import { token } from './api';
import { createStore, useStore } from './store';

// LiveEvent says a topic changed, in a project when it names one.
export interface LiveEvent { topic: string; project?: string }

type Listener = { event: (e: LiveEvent) => void; resume?: () => void };
const listeners = new Set<Listener>();
let source: EventSource | undefined;
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

// openSource keeps one live channel for every screen of the page and opens it again when the browser gives up or
// it falls silent; a channel it replaced says nothing more.
function openSource() {
  clearTimeout(retry);
  source?.close();
  const es = new EventSource('/api/events?token=' + encodeURIComponent(token()));
  source = es;
  const heard = () => {
    clearTimeout(watchdog);
    watchdog = setTimeout(() => {
      if (es !== source) return;
      setLost(true);
      openSource();
    }, SILENT);
  };
  heard();
  es.onopen = () => {
    if (es !== source) return;
    heard();
    if (lost.get()) resume();
    setLost(false);
  };
  es.addEventListener('ping', () => { if (es === source) heard(); });
  es.onmessage = (m) => {
    if (es !== source) return;
    heard();
    const e = JSON.parse(String(m.data)) as LiveEvent;
    listeners.forEach((l) => l.event(e));
  };
  es.onerror = () => {
    if (es !== source) return;
    setLost(true);
    if (es.readyState === EventSource.CLOSED && listeners.size) retry = setTimeout(openSource, 3000);
  };
}

// events follows the live channel of the page; the last one to leave closes it.
export function events(onEvent: (e: LiveEvent) => void, onResume?: () => void): () => void {
  const l: Listener = { event: onEvent, resume: onResume };
  listeners.add(l);
  if (!source || source.readyState === EventSource.CLOSED) openSource();
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
