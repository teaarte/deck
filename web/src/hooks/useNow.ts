import { useSyncExternalStore } from 'react';

// One clock for the page ticks every 15 s while anything follows it.
let now = Date.now();
let timer: ReturnType<typeof setInterval> | undefined;
const subs = new Set<() => void>();

function subscribe(f: () => void) {
  subs.add(f);
  timer ??= setInterval(() => {
    now = Date.now();
    subs.forEach((g) => g());
  }, 15000);
  return () => {
    subs.delete(f);
    if (subs.size || !timer) return;
    clearInterval(timer);
    timer = undefined;
  };
}
const still = () => () => undefined;

// read is the time of the clock; an idle clock catches up first, so a group that starts it never draws an old time.
const read = () => {
  if (!timer && Date.now() - now >= 1000) now = Date.now();
  return now;
};

// useNow is the time of the shared clock; with follow false it reads the time once and does not draw again.
export const useNow = (follow = true) => useSyncExternalStore(follow ? subscribe : still, read);
