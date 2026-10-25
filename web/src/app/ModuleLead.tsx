import { Group, Text } from '@mantine/core';
import { IconActivity, IconClockHour4 } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { MenuEntry } from '../hooks/useMenu';
import { formatted } from '../lib/format';
import { NavStatus } from './NavStatus';
import s from './shell.module.css';

// ModuleLead is the line of a module's screen: what it does now and runs next by its schedule, what waits and fails;
// a new task goes to Deki by its button, which knows the screen.
export function ModuleLead({ entry }: { entry: MenuEntry }) {
  const { t } = useTranslation();
  const state = { ...entry, running: entry.doing ? 0 : entry.running };
  return (
    <Group gap="md" wrap="wrap" miw={0} c="dimmed" className={s.lead} data-module-lead>
        {entry.doing && (
          <Group gap={6} wrap="nowrap" data-doing>
            <IconActivity size={15} className={s.leadIcon} data-tone="ok" aria-hidden />
            <Text size="sm" c="inherit">{t('lead.doing', { what: entry.doing })}</Text>
          </Group>
        )}
        <NavStatus m={state} words />
        {entry.next && (
          <Group gap={6} wrap="nowrap" data-next>
            <IconClockHour4 size={15} aria-hidden />
            <Text size="sm" c="inherit">{t('lead.next', { what: entry.next.title, when: formatted(entry.next.at, { format: 'relative' }) })}</Text>
          </Group>
        )}
    </Group>
  );
}
