import type { ReactNode } from 'react';
import { ActionIcon, Anchor, Badge, CopyButton, Text, Tooltip } from '@mantine/core';
import { IconCheck, IconCopy } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { formatted, show, tagColor, toneColor, type Shown } from '../lib/format';
import { useMinute } from '../hooks/useMinute';
import s_ from './value.module.css';

// Copied is a text with a button that copies it, shown on hover or a tap.
function Copied({ text }: { text: string }) {
  const { t } = useTranslation();
  return (
    <span className={s_.copy} tabIndex={-1} data-format="copy">
      {text}
      <CopyButton value={text}>{({ copied, copy }) => (
        <Tooltip label={copied ? t('actions.copied') : t('actions.copy')}>
          <ActionIcon className={s_.copyButton} variant="subtle" color="gray" size="xs" aria-label={t('actions.copy')}
            onClick={(e) => { e.stopPropagation(); copy(); }}>
            {copied ? <IconCheck size={12} /> : <IconCopy size={12} />}
          </ActionIcon>
        </Tooltip>
      )}</CopyButton>
    </span>
  );
}

// Value shows a value by its format: a link opens, a tag is a badge colored by its tone or else by its text (one for
// each value of a list), a relative time ages by the minute, a copy has a button that copies it.
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
  if (s.format === 'copy' && text !== '') return <Copied text={text} />;
  if (!s.format && !color) return text;
  return (
    <Text span inherit c={color} data-format={s.format} data-tone={tone ?? undefined} title={s.format === 'relative' ? show(v) : undefined}>{text}</Text>
  );
}
