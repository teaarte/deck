import { useRef, useSyncExternalStore } from 'react';
import { createStore } from '../lib/store';

// A clock of a period ticks for the whole page while anything follows it.
function clock(period: number) {
  const time = createStore(Date.now());
  let timer: ReturnType<typeof setInterval> | undefined;
  let follows = 0;
  return {
    // read catches an idle clock up first, so whoever starts it never draws an old time.
    read: () => {
      if (!timer && Date.now() - time.get() >= 1000) time.set(Date.now());
      return time.get();
    },
    subscribe: (f: () => void) => {
      const off = time.subscribe(f);
      follows++;
      timer ??= setInterval(() => time.set(Date.now()), period);
      return () => {
        off();
        if (--follows || !timer) return;
        clearInterval(timer);
        timer = undefined;
      };
    },
    wake: () => { if (timer) time.set(Date.now()); },
  };
}

const clocks = new Map<number, ReturnType<typeof clock>>();
const clockOf = (period: number) => {
  let c = clocks.get(period);
  if (!c) clocks.set(period, c = clock(period));
  return c;
};
document.addEventListener('visibilitychange', () => { if (!document.hidden) clocks.forEach((c) => c.wake()); });

const still = () => () => undefined;

// useClock is the time of the page's clock of a period, drawn again on its tick and on return to the page; with
// follow false it keeps the time it last drew.
export function useClock(period: number, follow = true): number {
  const c = clockOf(period);
  const now = useSyncExternalStore(follow ? c.subscribe : still, c.read);
  const held = useRef(now);
  if (follow) held.current = now;
  return held.current;
}
