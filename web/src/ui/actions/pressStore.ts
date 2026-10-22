import { call } from '../../lib/api';
import { createStore, useStore } from '../../lib/store';

// PressAsk is a call a run started by a button waits to make, as deck.process_asks gives it.
export interface PressAsk { id: number; what: string; where: string; who: string; command?: string; payload?: { call?: { input?: unknown } } }

const asks = createStore<PressAsk[]>([]);

// watchRun asks deck for the calls the run of a press waits to make, to answer them where the owner pressed.
export function watchRun(process: number) {
  call<PressAsk[]>('deck.process_asks', { process }).then((got) => {
    const fresh = (got ?? []).filter((a) => !asks.get().some((x) => x.id === a.id));
    if (fresh.length) asks.set([...asks.get(), ...fresh]);
  }, () => undefined);
}

// dropAsk takes an ask off the window once answered or left for the cockpit.
export const dropAsk = (id: number) => asks.set(asks.get().filter((a) => a.id !== id));

export const usePressAsks = () => useStore(asks);
