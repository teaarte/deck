import s from './dots.module.css';

// Dots are three dots that hop while something works.
export function Dots() {
  return <span className={s.dots} aria-hidden data-dots><i /><i /><i /></span>;
}
