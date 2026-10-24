import { Button, Tooltip } from '@mantine/core';
import { IconAlertCircleFilled } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { MenuEntry } from '../hooks/useMenu';
import { useMasterName } from '../ui/deki/Deki';
import { Glyph } from '../ui/Glyph';
import { useRoute } from './router';
import s from './master.module.css';

// MasterButton opens the chat; it shows whether the master works, waits for the owner or failed. It is filled only
// while its asks wait, the chat is closed and the cockpit, which shows them, is not open: one accent on a screen.
export function MasterButton({ m, open, onClick }: { m?: MenuEntry; open: boolean; onClick: () => void }) {
  const { t } = useTranslation();
  const name = useMasterName();
  const route = useRoute();
  const cockpit = !route.project && route.screen === 'cockpit';
  const busy = !!m?.running;
  const state = busy ? t('chat.working') : m?.asks ? t('nav.asks', { n: m.asks }) : m?.errors ? t('nav.errors', { n: m.errors }) : undefined;
  return (
    <Tooltip label={state ?? name} disabled={!state}>
      <Button size="xs" variant={m?.asks && !open && !cockpit ? 'filled' : 'light'} data-busy={busy || undefined} data-open={open || undefined}
        leftSection={<Glyph busy={busy} />} onClick={onClick} aria-label={state ? `${name}: ${state}` : undefined} data-chat-toggle
        rightSection={m?.asks ? <span className={s.masterAsks} data-master-asks>{m.asks}</span> : m?.errors ? <IconAlertCircleFilled size={14} color="var(--mantine-color-red-6)" aria-hidden /> : undefined}>
        {name}
      </Button>
    </Tooltip>
  );
}
