import { useSyncExternalStore } from 'react';

// One clock for the page ticks every 15 s while anything follows it.
let now = Date.now();
let timer: ReturnType<typeof setInterval> | undefined;
const subs = new Set<() => void>();

function subscribe(f: () => void) {
  subs.add(f);
  if (!timer) now = Date.now();
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

// useNow is the time of the shared clock; with follow false it reads the time once and does not draw again.
export const useNow = (follow = true) => useSyncExternalStore(follow ? subscribe : still, () => now);
