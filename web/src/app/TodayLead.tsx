import { Group, Text } from '@mantine/core';
import { IconAlertCircle, IconCheck, IconHandFinger, IconWorld } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { useMenuData } from '../hooks/useMenu';
import { locale } from '../lib/i18n';
import s from './shell.module.css';

const MARKS = { 'hand-finger': IconHandFinger, 'alert-circle': IconAlertCircle, check: IconCheck };

// TodayLine is the first line of Today: what is not seen below it, with its mark.
export function TodayLine() {
  const { pulse } = useMenuData();
  if (!pulse.line) return null;
  const Mark = MARKS[pulse.mark as keyof typeof MARKS] ?? IconCheck;
  return (
    <Group gap={10} wrap="wrap" data-today-line={pulse.mark}>
      <Group gap={10} wrap="nowrap">
        <span className={s.todayMark} data-mark={pulse.mark}><Mark size={16} /></span>
        <Text size="md" c="var(--deck-text-2)">{pulse.line}</Text>
      </Group>
      {pulse.opens && (
        <Group gap={6} wrap="nowrap" title={pulse.hosts} data-today-opens>
          <IconWorld size={16} />
          <Text size="sm" fw={600} c="var(--deck-text-2)">{pulse.opens}</Text>
        </Group>
      )}
    </Group>
  );
}

// TodayDate is the day of Today beside its first line, as the language of the interface says it.
export function TodayDate() {
  useTranslation();
  const day = new Date().toLocaleDateString(locale(), { weekday: 'long', day: 'numeric', month: 'long' });
  return <Text size="sm" c="dimmed" data-today-date>{day}</Text>;
}
