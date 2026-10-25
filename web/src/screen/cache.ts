import type { Message } from './feed';

// KEPT is how many screens the tab remembers.
const KEPT = 30;

// kept are the screens read in this tab by address and language, the last read first, as the messages that draw
// them now: a screen opened again shows them at once while it is read again.
const kept = new Map<string, Message[]>();

// remembered are the messages of a screen read before, none for one not read yet.
export function remembered(key: string): Message[] | undefined {
  const msgs = kept.get(key);
  if (msgs) {
    kept.delete(key);
    kept.set(key, msgs);
  }
  return msgs;
}

// remember keeps a read of a screen: a whole read in place of what was kept, a read of some blocks over it.
export function remember(key: string, msgs: Message[], whole: boolean) {
  const before = kept.get(key);
  if (!whole && !before) return;
  let all = msgs;
  if (!whole && before && !msgs.some((m) => m.updateComponents)) {
    const paths = new Set(msgs.map((m) => m.updateDataModel?.path).filter(Boolean));
    all = [...before.filter((m) => !paths.has(m.updateDataModel?.path)), ...msgs];
  }
  kept.delete(key);
  kept.set(key, all);
  while (kept.size > KEPT) kept.delete(kept.keys().next().value!);
}

// forget drops what was kept, as when the language changes.
export function forget() {
  kept.clear();
}
