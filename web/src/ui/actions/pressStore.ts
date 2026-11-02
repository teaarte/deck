import { call } from '../../lib/api';
import { liveRead } from '../../lib/live';
import i18n from '../../lib/i18n';
import type { Action } from '../../lib/catalog.gen';
import { createStore, useStore } from '../../lib/store';

// PressAsk is a call a run started by a button waits to make, as deck.process_asks_list gives it.
export interface PressAsk { id: number; what: string; where: string; who: string; command?: string; payload?: unknown; allow: Action; deny: Action }

const asks = createStore<PressAsk[]>([]);

// RunEnd is what the run of a press came to, as deck.process_asks_list gives it: the calls it waits to make, where it stands
// and why it failed.
export interface RunEnd { asks: PressAsk[]; state: string; result?: unknown; error?: string }

const runs = new Map<number, Promise<RunEnd | undefined>>();

// watchRun asks deck once what the run of a press came to, and puts the calls it waits to make where the owner pressed.
export function watchRun(process: number): Promise<RunEnd | undefined> {
  let got = runs.get(process);
  if (!got) {
    got = call<RunEnd>('deck.process_asks_list', { process, lang: i18n.language }).then((end) => {
      const fresh = (end?.asks ?? []).filter((a) => !asks.get().some((x) => x.id === a.id));
      if (fresh.length) asks.set([...asks.get(), ...fresh]);
      return end;
    }, () => undefined);
    runs.set(process, got);
    void got.then(() => setTimeout(() => runs.delete(process), 60000));
  }
  return got;
}

// keep leaves the cards of the asks still open, taking off those deck closed by itself: done already, or out of date.
const keep = (open: { id: number }[] | null) => {
  const ids = new Set((open ?? []).map((a) => a.id));
  asks.set(asks.get().filter((a) => ids.has(a.id)));
};

// settleAsks takes off the cards of asks deck closed by itself meanwhile.
export function settleAsks() {
  if (asks.get().length) call<{ id: number }[]>('deck.asks_list', { fields: ['id'] }).then(keep, () => undefined);
}

// followAsks keeps the cards to the asks still open while they are shown; it returns its stop.
export const followAsks = () => liveRead<{ id: number }[]>('deck.asks_list', { fields: ['id'] }, keep);

// dropAsk takes an ask off the window once answered or left for the cockpit.
export const dropAsk = (id: number) => asks.set(asks.get().filter((a) => a.id !== id));

export const usePressAsks = () => useStore(asks);
