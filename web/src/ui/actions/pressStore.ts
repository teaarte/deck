import { useSyncExternalStore } from 'react';
import { call } from '../../lib/api';

// PressAsk is a call a run started by a button waits to make, as deck.process_asks gives it.
export interface PressAsk { id: number; what: string; where: string; who: string; command?: string }

let asks: PressAsk[] = [];
const subs = new Set<() => void>();
const publish = (next: PressAsk[]) => {
  asks = next;
  subs.forEach((f) => f());
};

// watchRun asks deck for the calls the run of a press waits to make, to answer them where the owner pressed.
export function watchRun(process: number) {
  call<PressAsk[]>('deck.process_asks', { process }).then((got) => {
    const fresh = (got ?? []).filter((a) => !asks.some((x) => x.id === a.id));
    if (fresh.length) publish([...asks, ...fresh]);
  }, () => undefined);
}

// dropAsk takes an ask off the window once answered or left for the cockpit.
export const dropAsk = (id: number) => publish(asks.filter((a) => a.id !== id));

export const usePressAsks = () => useSyncExternalStore((f) => (subs.add(f), () => subs.delete(f)), () => asks);
