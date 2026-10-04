import { useSyncExternalStore } from 'react';
import { token } from './api';

// LiveEvent says a topic changed, in a project when it names one.
export interface LiveEvent { topic: string; project?: string }

type Listener = { event: (e: LiveEvent) => void; resume?: () => void };
const listeners = new Set<Listener>();
let source: EventSource | undefined;
let retry: ReturnType<typeof setTimeout> | undefined;
// lost is set while a live channel that was open is down; screens drawn meanwhile may be old.
let lost = false;
const lostSubs = new Set<() => void>();
const setLost = (v: boolean) => { lost = v; lostSubs.forEach((f) => f()); };
export const useLive = () => !useSyncExternalStore((f) => (lostSubs.add(f), () => lostSubs.delete(f)), () => lost);

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

// openSource keeps one live channel for every screen of the page and opens it again when the browser gives up;
// a channel it replaced says nothing more.
function openSource() {
  clearTimeout(retry);
  source?.close();
  const es = new EventSource('/api/events?token=' + encodeURIComponent(token()));
  source = es;
  es.onopen = () => {
    if (es !== source) return;
    if (lost) resume();
    setLost(false);
  };
  es.onmessage = (m) => {
    if (es !== source) return;
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
    source?.close();
    source = undefined;
    setLost(false);
  };
}
