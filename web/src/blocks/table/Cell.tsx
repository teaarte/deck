import { useState, type ReactNode } from 'react';
import s from './table.module.css';

// Cell holds a value in at most three lines; a click on a cut one shows it whole.
export function Cell({ children }: { children: ReactNode }) {
  const [whole, setWhole] = useState(false);
  return (
    <div className={s.clamp} data-whole={whole || undefined} onClick={(e) => {
      const el = e.currentTarget;
      if (!whole && el.scrollHeight > el.clientHeight + 1) { e.stopPropagation(); setWhole(true); }
    }}>{children}</div>
  );
}
