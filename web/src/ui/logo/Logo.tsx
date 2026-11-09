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

// markOf is the rule of the first word of names that starts it followed by no letter, such as gpt-4o or qwen3: its
// start and SVG.
export function markOf(names: string, rules: [string, string][]): [string, string] | undefined {
  for (const word of names.toLowerCase().split(/[\s/@:_,]+/).filter(Boolean)) {
    for (const rule of rules) {
      if (word.startsWith(rule[0]) && !/[a-z]/.test(word.charAt(rule[0].length))) return rule;
    }
  }
  return undefined;
}

// Logo is the mark of a model, its maker or provider by words that name them, else the first letter (none without
// letter); via names the provider it goes through, whose own mark stands small in its corner.
export function Logo({ names, via, size = 18, letter = true }: { names: string; via?: string; size?: number; letter?: boolean }) {
  const rules = useStore(marks);
  useEffect(() => { if (rules === undefined) load(); }, [rules]);
  const [key, svg] = (rules && markOf(names, rules)) || [];
  const [byKey, by] = (rules && via && markOf(via, rules)) || [];
  if (!svg && !letter) return null;
  const mark = svg
    ? <span className={s.mark} style={{ width: size, height: size, fontSize: size }} data-logo={key} aria-hidden dangerouslySetInnerHTML={{ __html: svg }} />
    : (
      <Box component="span" className={s.letter} w={size} h={size} fz={size * 0.6} data-logo-letter aria-hidden>
        {rules === undefined ? '' : names.trim().slice(0, 1)}
      </Box>
    );
  if (!by || by === svg) return mark;
  const small = Math.max(8, Math.round(size * 0.6));
  return (
    <span className={s.pair} data-logo-pair>
      {mark}
      <span className={s.via} style={{ width: small, height: small }} data-logo-via={byKey} aria-hidden dangerouslySetInnerHTML={{ __html: by }} />
    </span>
  );
}
