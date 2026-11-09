import { useState } from 'react';
import { Text, UnstyledButton } from '@mantine/core';
import { IconArrowUpRight, IconChevronDown, IconChevronUp } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { Running } from '../lib/catalog.gen';
import { useClock } from '../hooks/useClock';
import { clock, toneColor } from '../lib/format';
import { ActionIconButton } from '../ui/actions/ActionIconButton';
import { Logo } from '../ui/logo/Logo';
import { NamedIcon } from '../ui/NamedIcon';
import s from './chat.module.css';

// Pill is a work of the topic: its module's icon, command and model mark, how long it runs or what it waits for,
// or how it ended, and the button that opens it.
function Pill({ p, now, icon, ended }: { p: Running; now: number; icon?: string; ended?: boolean }) {
  const started = Date.parse(p.started_at);
  const fact = ended || p.waits || Number.isNaN(started) ? p.word : clock((now - started) / 1000);
  return (
    <span className={s.pill} data-chat-work-pill={p.id} data-waits={p.waits || undefined} data-ended={ended || undefined}>
      {icon && <NamedIcon name={icon} fallback={p.project} size={13} />}
      <Text span size="xs" ff="monospace" className={s.pillName}>{p.project}.{p.command}</Text>
      {p.logo && <span title={p.model} aria-label={p.model} role="img"><Logo names={p.logo} size={12} /></span>}
      <Text span size="xs" c={ended ? toneColor(p.tone) ?? 'dimmed' : p.waits ? undefined : 'dimmed'} className={s.pillFact} data-chat-work-fact>{fact}</Text>
      <ActionIconButton act={p.open} variant="subtle" icon={<IconArrowUpRight size={13} />} />
    </span>
  );
}

// WorkStrip is the work the replies of the topic started while it goes on, above the input: how many, a pill each, and
// the recent that ended within the hour folded behind a button.
export function WorkStrip({ running, recent, iconOf }: { running: Running[]; recent: Running[]; iconOf: (project?: string) => string | undefined }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const now = useClock(1000, running.length > 0);
  if (!running.length) return null;
  const waits = running.some((p) => p.waits);
  return (
    <div className={s.workBox}>
      <div className={s.workStrip} role="status" aria-label={t('Running')} data-chat-work={running.length}>
        <span className={s.workCount}>
          <span className={s.busyDot} data-waits={waits || undefined} aria-hidden />
          <Text span size="xs" fw={600} className={s.workLabel}>{t('Running')}</Text>
          <Text span size="xs" fw={700}>{running.length}</Text>
        </span>
        {running.map((p) => <Pill key={p.id} p={p} now={now} icon={iconOf(p.project)} />)}
        {open && recent.map((p) => <Pill key={p.id} p={p} now={now} icon={iconOf(p.project)} ended />)}
        {recent.length > 0 && (
          <UnstyledButton className={s.workRecent} onClick={() => setOpen(!open)} aria-expanded={open} data-chat-work-recent={recent.length}>
            <Text span size="xs" c="dimmed">{t('Recent')}</Text>
            {open ? <IconChevronUp size={13} /> : <IconChevronDown size={13} />}
          </UnstyledButton>
        )}
      </div>
    </div>
  );
}
