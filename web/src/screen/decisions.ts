import { useSyncExternalStore } from 'react';
import type { Item } from '../blocks/list';

export interface Decisions { id: string; items: Item[] }

// The lists of the screen with buttons that call commands, top first: the first is what the owner goes through one by
// one.
const lists = new Map<string, { items: Item[]; at: Element }>();
let ordered: Decisions[] = [];
const subs = new Set<() => void>();
const publish = () => {
  ordered = [...lists.entries()].sort(([, a], [, b]) => (a.at.compareDocumentPosition(b.at) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1))
    .map(([id, l]) => ({ id, items: l.items }));
  subs.forEach((f) => f());
};

const decides = (items: Item[]) => items.some((it) => it.actions?.some((a) => a.command));

// keepList holds the items of a list while it is on the screen; a list without such buttons is not one to go through.
export function keepList(id: string, items: Item[], at: Element) {
  if (decides(items)) lists.set(id, { items, at });
  else if (!lists.delete(id)) return;
  publish();
}

export function dropList(id: string) {
  if (lists.delete(id)) publish();
}

// useDecisionLists are the lists to go through, top first.
export const useDecisionLists = () => useSyncExternalStore((f) => (subs.add(f), () => subs.delete(f)), () => ordered);
