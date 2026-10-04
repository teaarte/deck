import { useSyncExternalStore } from 'react';

let minute = 0;
const subs = new Set<() => void>();
const tick = () => {
  minute++;
  subs.forEach((f) => f());
};
setInterval(tick, 60000);
document.addEventListener('visibilitychange', () => { if (!document.hidden) tick(); });
const subscribe = (f: () => void) => (subs.add(f), () => subs.delete(f));
const still = () => () => undefined;

// useMinute draws again once a minute and on return to the page, so relative times age without a read of the server.
export const useMinute = (follow = true) => useSyncExternalStore(follow ? subscribe : still, () => minute);
