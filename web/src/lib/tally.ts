// tally counts the draws of a part of the client by name in window.deckDraws, which tests read to keep them few.
export function tally(name: string) {
  const w = window as unknown as { deckDraws?: Record<string, number> };
  w.deckDraws ??= {};
  w.deckDraws[name] = (w.deckDraws[name] ?? 0) + 1;
}
