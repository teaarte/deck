import { Group, Text } from '@mantine/core';
import { IconAlertCircle, IconCheck, IconHandFinger } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { useMenuData } from '../hooks/useMenu';
import s from './shell.module.css';

const MARKS = { 'hand-finger': IconHandFinger, 'alert-circle': IconAlertCircle, check: IconCheck };

// TodayLine is the first line of Today: what is not seen below it, with its mark.
export function TodayLine() {
  const { pulse } = useMenuData();
  if (!pulse.line) return null;
  const Mark = MARKS[pulse.mark as keyof typeof MARKS] ?? IconCheck;
  return (
    <Group gap={10} wrap="nowrap" data-today-line={pulse.mark}>
      <span className={s.todayMark} data-mark={pulse.mark}><Mark size={16} /></span>
      <Text size="md" c="var(--deck-text-2)">{pulse.line}</Text>
    </Group>
  );
}

// TodayDate is the day of Today beside its first line, as the language of the interface says it.
export function TodayDate() {
  const { i18n } = useTranslation();
  const day = new Date().toLocaleDateString(i18n.language, { weekday: 'long', day: 'numeric', month: 'long' });
  return <Text size="sm" c="dimmed" data-today-date>{day}</Text>;
}
