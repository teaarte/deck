import { call } from './api';
import { events } from './events';

// liveRead reads a command now, again 400 ms after a change of a topic its last answer follows and on return to the
// page; each answer goes to onData, a failed read to onError and keeps what was read. It returns its stop.
export function liveRead<T>(command: string, input: Record<string, unknown>, topics: (data: T | undefined) => string[],
  onData: (data: T) => void, onError?: (e: unknown) => void): () => void {
  let last: T | undefined;
  let stopped = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const read = () => {
    call<T>(command, input).then((d) => { if (!stopped) { last = d; onData(d); } }, (e) => { if (!stopped) onError?.(e); });
  };
  const off = events((e) => {
    if (timer || !topics(last).includes(e.topic)) return;
    timer = setTimeout(() => { timer = undefined; read(); }, 400);
  }, read);
  read();
  return () => { stopped = true; clearTimeout(timer); off(); };
}
