import { Button, Tooltip } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { MenuEntry } from '../hooks/useMenu';
import { useMasterName } from '../ui/deki/Deki';
import { Glyph } from '../ui/Glyph';
import n from './nav.module.css';
import s from './master.module.css';

// MasterButton opens the chat; it shows whether the master works, waits for the owner or failed.
export function MasterButton({ m, open, onClick }: { m?: MenuEntry; open: boolean; onClick: () => void }) {
  const { t } = useTranslation();
  const name = useMasterName();
  const busy = !!m?.running;
  const state = busy ? t('chat.working') : m?.asks ? t('nav.asks', { n: m.asks }) : m?.errors ? t('nav.errors', { n: m.errors }) : undefined;
  return (
    <Tooltip label={state ?? name} disabled={!state}>
      <Button size="xs" radius="xl" variant="filled" className={s.master} data-busy={busy || undefined} data-open={open || undefined}
        leftSection={<Glyph busy={busy} />} onClick={onClick} aria-label={state ? `${name}: ${state}` : undefined} data-chat-toggle
        rightSection={m?.asks ? <span className={s.masterAsks} data-master-asks>{m.asks}</span> : m?.errors ? <span className={n.dot} /> : undefined}>
        {name}
      </Button>
    </Tooltip>
  );
}
