// Cell is where a card stands on the tiles, in cells: Today or a module by its name.
export type Cell = { i: string; x: number; y: number; w: number; h: number };

// Saved is the layout deck gives a device: of its columns, or 0 when it gives the order alone.
export type Saved = { columns: number; own: boolean; cards: { id: string; x: number; y: number; w: number; h: number }[] };

export const TODAY = 'today';

// least is the smallest size of a card: Today takes two cells of a row where the grid has them.
export const least = (id: string, cols: number) => ({ w: id === TODAY ? Math.min(2, cols) : 1, h: 1 });

// derive lays the cards out for a number of columns from the layout deck gave: a card that fits stays where it
// stood, the rest go to the first free cells in reading order, then the cards it does not name; nothing is saved.
export function derive(saved: Saved, ids: string[], cols: number): Cell[] {
  const taken = new Set<string>();
  const free = (x: number, y: number, w: number, h: number) => {
    for (let dy = 0; dy < h; dy++) for (let dx = 0; dx < w; dx++) if (taken.has(`${x + dx},${y + dy}`)) return false;
    return true;
  };
  const take = (c: Cell) => {
    for (let dy = 0; dy < c.h; dy++) for (let dx = 0; dx < c.w; dx++) taken.add(`${c.x + dx},${c.y + dy}`);
    return c;
  };
  const size = (id: string, w: number, h: number) => {
    const min = least(id, cols);
    return { w: Math.min(cols, Math.max(min.w, w || min.w)), h: Math.max(min.h, h || min.h) };
  };
  const out: Cell[] = [];
  const rest: Cell[] = [];
  const known = saved.cards.filter((c) => ids.includes(c.id));
  for (const c of known) {
    const { w, h } = size(c.id, c.w, c.h);
    const cell = { i: c.id, x: c.x, y: c.y, w, h };
    if (saved.columns > 0 && c.x + w <= cols && free(c.x, c.y, w, h)) out.push(take(cell));
    else rest.push(cell);
  }
  for (const id of ids) {
    if (known.some((c) => c.id === id)) continue;
    // Today a layout does not name goes first, as on a deck nobody placed.
    if (id === TODAY) rest.unshift({ i: id, x: 0, y: 0, ...size(id, 0, 0) });
    else rest.push({ i: id, x: 0, y: 0, ...size(id, 0, 0) });
  }
  for (const c of rest) {
    for (let y = 0; ; y++) {
      const x = Array.from({ length: cols - c.w + 1 }, (_, k) => k).find((k) => free(k, y, c.w, c.h));
      if (x !== undefined) {
        out.push(take({ ...c, x, y }));
        break;
      }
    }
  }
  return out;
}
