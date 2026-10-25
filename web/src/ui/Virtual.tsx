import { useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';

// LONG is how many items a list draws whole; a longer one draws only the rows near the view.
const LONG = 60;

// scroller is the nearest ancestor of el that scrolls, else the page.
function scroller(el: HTMLElement | null): HTMLElement {
  for (let p = el?.parentElement; p; p = p.parentElement) {
    const y = getComputedStyle(p).overflowY;
    if ((y === 'auto' || y === 'scroll') && p.scrollHeight > p.clientHeight) return p;
  }
  return document.scrollingElement as HTMLElement;
}

// Virtual draws a long list of items in rows of columns, only the rows near the view, each measured as it draws; a
// grid takes as many columns of width as fit. A short list draws whole.
export function Virtual<T>({ items, render, row, width, gap = 8, className, style }: {
  items: T[]; render: (it: T, i: number) => ReactNode; row: number; width?: number; gap?: number; className?: string;
  style?: React.CSSProperties;
}) {
  const box = useRef<HTMLDivElement>(null);
  const [columns, setColumns] = useState(1);
  const [margin, setMargin] = useState(0);
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    const fit = () => {
      setColumns(width ? Math.max(1, Math.floor((el.clientWidth + gap) / (width + gap))) : 1);
      const s = scroller(el);
      setMargin(el.getBoundingClientRect().top - s.getBoundingClientRect().top + s.scrollTop);
    };
    fit();
    const o = new ResizeObserver(fit);
    o.observe(el);
    return () => o.disconnect();
  }, [width, gap]);
  const rows = Math.ceil(items.length / columns);
  const v = useVirtualizer({
    count: items.length > LONG ? rows : 0, estimateSize: () => row + gap, overscan: 4, scrollMargin: margin,
    getScrollElement: () => scroller(box.current),
  });
  const grid = width ? { display: 'grid', gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))`, gap } : undefined;
  if (items.length <= LONG) {
    return <div ref={box} className={className} style={{ ...style, ...grid }}>{items.map(render)}</div>;
  }
  return (
    <div ref={box} className={className} style={{ ...style, height: v.getTotalSize(), position: 'relative' }} data-virtual>
      {v.getVirtualItems().map((r) => (
        <div key={r.key} ref={v.measureElement} data-index={r.index}
          style={{ position: 'absolute', top: 0, left: 0, right: 0, transform: `translateY(${r.start - v.options.scrollMargin}px)`, paddingBottom: gap, ...grid }}>
          {items.slice(r.index * columns, r.index * columns + columns).map((it, j) => render(it, r.index * columns + j))}
        </div>
      ))}
    </div>
  );
}
