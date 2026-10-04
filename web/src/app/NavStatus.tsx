import { Badge, Group, Tooltip } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { MenuEntry } from '../hooks/useMenu';
import s from '../shell.module.css';

// NavStatus shows what a project is busy with: running processes, questions to the owner, failing health.
export function NavStatus({ m }: { m: MenuEntry }) {
  const { t } = useTranslation();
  return (
    <Group gap={6} wrap="nowrap" data-status>
      {!!m.errors && <Tooltip label={t('nav.errors', { n: m.errors })}><span className={s.dot} role="img" aria-label={t('nav.errors', { n: m.errors })} data-errors={m.errors} /></Tooltip>}
      {!!m.asks && <Tooltip label={t('nav.asks', { n: m.asks })}><Badge size="sm" circle color="orange" aria-label={t('nav.asks', { n: m.asks })} data-asks={m.asks}>{m.asks}</Badge></Tooltip>}
      {!!m.running && <Tooltip label={t('nav.running', { n: m.running })}><span className={s.pulse} role="img" aria-label={t('nav.running', { n: m.running })} data-running={m.running} /></Tooltip>}
    </Group>
  );
}
