import type { ReactNode } from 'react';
import { Anchor, Badge, Text } from '@mantine/core';
import { formatted, show, toneColor, type Shown } from '../lib/format';
import { useMinute } from '../hooks/useMinute';

// Value shows a value by its format: a link opens, a tag is a badge, a relative time ages by the minute.
export function Value({ v, s, tone }: { v: unknown; s: Shown; tone?: string | null }): ReactNode {
  useMinute(s.format === 'relative');
  const color = toneColor(tone);
  if (s.format === 'link' && typeof v === 'string' && /^https?:\/\//.test(v)) {
    return <Anchor href={v} target="_blank" rel="noopener noreferrer" size="sm" c={color}>{v}</Anchor>;
  }
  if (s.format === 'tag' && v !== null && v !== undefined && v !== '') {
    return <Badge variant="light" color={color === 'dimmed' ? 'gray' : color ?? 'gray'} data-format="tag" data-tone={tone ?? undefined} miw="max-content">{show(v)}</Badge>;
  }
  const text = formatted(v, s);
  if (!s.format && !color) return text;
  return (
    <Text span inherit c={color} data-format={s.format} data-tone={tone ?? undefined} title={s.format === 'relative' ? show(v) : undefined}>{text}</Text>
  );
}
