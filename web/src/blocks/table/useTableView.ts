import { useMemo, useState } from 'react';
import { text } from '../../lib/format';
import { locale } from '../../lib/i18n';
import type { Data } from '../../lib/catalog.gen';

export type Row = NonNullable<Data['rows']>[number];

function compare(a: unknown, b: unknown): number {
  if (typeof a === 'number' && typeof b === 'number') return a - b;
  return text(a).localeCompare(text(b), locale(), { numeric: true, sensitivity: 'base' });
}

// useTableView is what a table shows of its rows: those the search finds, in the order of the sorted column, a page
// at a time.
export function useTableView(rows: Row[]) {
  const [query, setQuery] = useState('');
  const [sort, setSort] = useState<{ col: number; desc: boolean }>();
  const [size, setSize] = useState('25');
  const [page, setPage] = useState(1);
  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    let out = q ? rows.filter((r) => r.cells.some((c) => text(c).toLowerCase().includes(q))) : rows;
    if (sort) out = [...out].sort((a, b) => compare(a.cells[sort.col], b.cells[sort.col]) * (sort.desc ? -1 : 1));
    return out;
  }, [rows, query, sort]);
  const per = Number(size);
  const pages = Math.max(1, Math.ceil(shown.length / per));
  const at = Math.min(page, pages);
  return {
    query, sort, size, per, pages, at, shown,
    slice: shown.slice((at - 1) * per, at * per),
    search: (q: string) => { setQuery(q); setPage(1); },
    resize: (v: string) => { setSize(v); setPage(1); },
    flip: (col: number) => setSort((x) => (x?.col !== col ? { col, desc: false } : x.desc ? undefined : { col, desc: true })),
    setPage,
  };
}
