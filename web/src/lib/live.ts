import { invoke } from './api';
import { events } from './events';

// liveRead reads a command now, again 400 ms after a change of a topic its last answer named and on return to the
// page; a new answer goes to onData, a failed read to onError, keeping what was read. It returns its stop.
export function liveRead<T>(command: string, input: Record<string, unknown>, onData: (data: T) => void,
  onError?: (e: unknown) => void): () => void {
  let topics: string[] = [];
  let said = '';
  let stopped = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const read = () => {
    invoke<T>(command, input).then((a) => {
      topics = a.topics;
      const now = JSON.stringify(a.data);
      if (stopped || now === said) return;
      said = now;
      onData(a.data);
    }, (e) => { if (!stopped) onError?.(e); });
  };
  const off = events((e) => {
    if (timer || !topics.includes(e.topic)) return;
    timer = setTimeout(() => { timer = undefined; read(); }, 400);
  }, read);
  read();
  return () => { stopped = true; clearTimeout(timer); off(); };
}
