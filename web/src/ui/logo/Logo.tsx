import { useEffect } from 'react';
import { Box } from '@mantine/core';
import { createStore, useStore } from '../../lib/store';
import s from './Logo.module.css';

// The marks load once, in a chunk of their own, when a line first shows a logo; null while the last load failed.
const marks = createStore<[string, string][] | null | undefined>(undefined);
let loading: Promise<void> | undefined;
function load() {
  loading ??= import('./marks').then((m) => marks.set(m.rules), () => {
    loading = undefined;
    marks.set(null);
  });
}

// markOf is the SVG of the first word of names that starts a rule followed by no letter, such as gpt-4o or qwen3.
export function markOf(names: string, rules: [string, string][]): string | undefined {
  for (const word of names.toLowerCase().split(/[\s/@:_,]+/).filter(Boolean)) {
    for (const [key, svg] of rules) {
      if (word.startsWith(key) && !/[a-z]/.test(word.charAt(key.length))) return svg;
    }
  }
  return undefined;
}

// Logo is the mark of a model, its maker or provider by words that name them, else the first letter of the first word.
export function Logo({ names, size = 18 }: { names: string; size?: number }) {
  const rules = useStore(marks);
  useEffect(() => { if (rules === undefined) load(); }, [rules]);
  const svg = rules ? markOf(names, rules) : undefined;
  if (svg) return <span className={s.mark} style={{ width: size, height: size, fontSize: size }} data-logo aria-hidden dangerouslySetInnerHTML={{ __html: svg }} />;
  return (
    <Box component="span" className={s.letter} w={size} h={size} fz={size * 0.6} data-logo-letter aria-hidden>
      {rules === undefined ? '' : names.trim().slice(0, 1)}
    </Box>
  );
}
