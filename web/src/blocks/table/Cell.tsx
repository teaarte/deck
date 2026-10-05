import { useState, type ReactNode } from 'react';
import { Details } from '../../ui/Details';
import s from './table.module.css';

// Cell holds a value in at most three lines; a click on a cut one shows it whole. A cell with details shows its
// value in one line and the details in full on demand.
export function Cell({ children, details }: { children: ReactNode; details?: string | null }) {
  const [whole, setWhole] = useState(false);
  return (
    <>
      <div className={s.clamp} data-one={details ? true : undefined} data-whole={whole || undefined} onClick={(e) => {
        const el = e.currentTarget;
        if (!whole && el.scrollHeight > el.clientHeight + 1) { e.stopPropagation(); setWhole(true); }
      }}>{children}</div>
      {details && <div onClick={(e) => e.stopPropagation()} data-cell-details><Details text={details} of="text" /></div>}
    </>
  );
}
