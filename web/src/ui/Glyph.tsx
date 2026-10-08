import { useEffect, useState } from 'react';
import { IconSparkles } from '@tabler/icons-react';
import s from './aura.module.css';

const GLYPHS = ['✦', '✧', '✶', '✷', '✸', '✹', '✺', '✹', '✸', '✷', '✶', '✧'];

// Glyph is the mark of the master: still when it rests, a cycle of stars while it works.
export function Glyph({ busy }: { busy: boolean }) {
  const [i, setI] = useState(0);
  useEffect(() => {
    if (!busy || matchMedia('(prefers-reduced-motion: reduce)').matches) return;
    const timer = setInterval(() => setI((n) => (n + 1) % GLYPHS.length), 110);
    return () => clearInterval(timer);
  }, [busy]);
  if (!busy) return <IconSparkles size={14} />;
  return <span className={s.glyph} aria-hidden>{GLYPHS[i]}</span>;
}
