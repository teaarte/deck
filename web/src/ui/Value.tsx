import type { ReactNode } from 'react';
import { Anchor, Badge, Text } from '@mantine/core';
import { formatted, show, tagColor, toneColor, type Shown } from '../lib/format';
import { useMinute } from '../hooks/useMinute';

// Value shows a value by its format: a link opens, a tag is a badge colored by its tone or else by its text (one for
// each value of a list), a relative time ages by the minute.
export function Value({ v, s, tone }: { v: unknown; s: Shown; tone?: string | null }): ReactNode {
  useMinute(s.format === 'relative');
  const color = toneColor(tone);
  if (s.format === 'link' && typeof v === 'string' && /^https?:\/\//.test(v)) {
    return <Anchor href={v} target="_blank" rel="noopener noreferrer" size="sm" c={color}>{v}</Anchor>;
  }
  if (s.format === 'tag' && Array.isArray(v)) {
    return <>{v.map((x, i) => <Value key={i} v={x} s={s} tone={tone} />)}</>;
  }
  if (s.format === 'tag' && v !== null && v !== undefined && v !== '') {
    return <Badge variant="light" color={color === 'dimmed' ? 'gray' : color ?? tagColor(v)} data-format="tag" data-tone={tone ?? undefined} miw="max-content">{show(v)}</Badge>;
  }
  const text = formatted(v, s);
  if (!s.format && !color) return text;
  return (
    <Text span inherit c={color} data-format={s.format} data-tone={tone ?? undefined} title={s.format === 'relative' ? show(v) : undefined}>{text}</Text>
  );
}
