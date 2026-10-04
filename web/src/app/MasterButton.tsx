import { useEffect, useState } from 'react';
import { Button, Tooltip } from '@mantine/core';
import { IconSparkles } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { MenuEntry } from '../hooks/useMenu';
import n from './nav.module.css';
import s from './master.module.css';

const GLYPHS = ['✦', '✧', '✶', '✷', '✸', '✹', '✺', '✹', '✸', '✷', '✶', '✧'];

// Glyph is the mark of the master: still when it rests, a cycle of stars while it works.
function Glyph({ busy }: { busy: boolean }) {
  const [i, setI] = useState(0);
  useEffect(() => {
    if (!busy || matchMedia('(prefers-reduced-motion: reduce)').matches) return;
    const timer = setInterval(() => setI((n) => (n + 1) % GLYPHS.length), 110);
    return () => clearInterval(timer);
  }, [busy]);
  if (!busy) return <IconSparkles size={14} />;
  return <span className={s.glyph} aria-hidden>{GLYPHS[i]}</span>;
}

// MasterButton opens the chat; it shows whether the master works, waits for the owner or failed.
export function MasterButton({ m, open, onClick }: { m?: MenuEntry; open: boolean; onClick: () => void }) {
  const { t } = useTranslation();
  const busy = !!m?.running;
  const state = busy ? t('chat.working') : m?.asks ? t('nav.asks', { n: m.asks }) : m?.errors ? t('nav.errors', { n: m.errors }) : undefined;
  return (
    <Tooltip label={state ?? t('chat.master')} disabled={!state}>
      <Button size="xs" radius="xl" variant="filled" className={s.master} data-busy={busy || undefined} data-open={open || undefined}
        leftSection={<Glyph busy={busy} />} onClick={onClick} aria-label={state ? `${t('chat.master')}: ${state}` : undefined} data-chat-toggle
        rightSection={m?.asks ? <span className={s.masterAsks} data-master-asks>{m.asks}</span> : m?.errors ? <span className={n.dot} /> : undefined}>
        {t('chat.master')}
      </Button>
    </Tooltip>
  );
}
