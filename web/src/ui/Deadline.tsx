import { IconClock } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { useClock } from '../hooks/useClock';
import s from './chip.module.css';

// Deadline is the time left before until: red under half an hour, amber under an hour.
export function Deadline({ until }: { until: string }) {
  const { t } = useTranslation();
  const now = useClock(15000);
  const left = Math.max(0, Math.round((Date.parse(until) - now) / 60000));
  if (Number.isNaN(left)) return null;
  const tone = left < 30 ? 'error' : left < 60 ? 'warn' : undefined;
  const h = Math.floor(left / 60);
  const text = h ? t('deadline.hours', { h, m: left % 60 }) : t('deadline.minutes', { m: left });
  return <span className={s.chip} data-tone={tone} data-deadline><IconClock size={12} stroke={2} />{text}</span>;
}
