import { ActionIcon, Group, Text, Tooltip } from '@mantine/core';
import { useLocalStorage } from '@mantine/hooks';
import { IconArrowUpRight, IconChevronDown, IconChevronUp, IconPlayerStopFilled } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { ChatValue } from '../lib/catalog.gen';
import { useClock } from '../hooks/useClock';
import { formatted, toneColor } from '../lib/format';
import { ActionIconButton } from '../ui/actions/ActionIconButton';
import { Deki, useMascot } from '../ui/deki/Deki';
import { Glyph } from '../ui/Glyph';
import { usePress } from '../ui/actions/usePress';
import s from './chat.module.css';

type TurnValue = NonNullable<ChatValue['turn']>;
type Proc = NonNullable<TurnValue['processes']>[number];

const SHOWN = 6;

// logLines are the calls and messages of the log as lines, a call marked by how it ended.
export function logLines(lines: TurnValue['lines'], shown = SHOWN): { key: string; text: string; failed?: boolean }[] {
  const ended = new Map(lines.filter((l) => l.kind === 'tool_update' && l.id).map((l) => [l.id!, l.status]));
  return lines.filter((l) => l.kind !== 'tool_update').map((l, i) => {
    if (l.kind !== 'tool') return { key: String(i), text: (l.text ?? '').split('\n').find((x) => x.trim()) ?? '' };
    const status = l.id ? ended.get(l.id) : undefined;
    return { key: String(i), text: `${status === 'failed' ? '✗' : status ? '✓' : '›'} ${l.title ?? ''}`, failed: status === 'failed' };
  }).filter((l) => l.text.trim()).slice(-shown);
}

// Processes are the processes a reply started, each with its state in the color of its tone, and the button that
// opens its log.
export function Processes({ list }: { list: Proc[] }) {
  if (!list.length) return null;
  return (
    <div className={s.procs} data-turn-processes>
      {list.map((p) => (
        <Group key={p.id} gap={6} wrap="nowrap" className={s.proc} data-process={p.id}>
          <Text size="xs" ff="monospace" truncate style={{ flex: 1 }}>{p.project}.{p.command}</Text>
          <Text size="xs" c={toneColor(p.tone) ?? 'dimmed'} data-process-tone={p.tone}>{p.word}</Text>
          <ActionIconButton act={p.open} variant="subtle" icon={<IconArrowUpRight size={13} />} />
        </Group>
      ))}
    </div>
  );
}

// Turn is the reply going on: what it does or waits for over a soft drift, the tail of its log that opens the whole,
// and its buttons; folded, only the line that says what it does.
export function Turn({ turn }: { turn: TurnValue }) {
  const { t } = useTranslation();
  const [folded, setFolded] = useLocalStorage({ key: 'deck-chat-turn-folded', defaultValue: false });
  const open = usePress(turn.open ?? {});
  const running = turn.state === 'running';
  const mascot = useMascot();
  const lines = logLines(turn.lines);
  const now = useClock(1000, running);
  const started = turn.started_at ? Date.parse(turn.started_at) : NaN;
  const steps = turn.lines.filter((l) => l.kind === 'tool').length;
  const buttons = (
    <>
      {turn.stop && <ActionIconButton act={turn.stop} color="red" variant="light" icon={<IconPlayerStopFilled size={13} />} />}
      {turn.open && <ActionIconButton act={turn.open} variant="default" icon={<IconArrowUpRight size={15} />} />}
    </>
  );
  return (
    <div className={s.turn} data-turn={turn.state} data-folded={folded || undefined}>
      <div className={s.turnHead} data-live={running || undefined}>
        <span className={s.turnMark}>{mascot ? <Deki mood={running ? 'dots' : 'wait'} size={26} still /> : <Glyph busy={running} />}</span>
        <Text size="xs" fw={500} truncate style={{ flex: 1 }}>
          {running ? t(mascot ? 'chat.replying_deki' : 'chat.replying', { provider: turn.provider ?? '' }) : turn.word}
        </Text>
        {!Number.isNaN(started) && (
          <Text size="xs" c="dimmed" data-turn-timer>
            {formatted(Math.max(0, (now - started) / 1000), { format: 'duration' })}{steps > 0 ? ' · ' + t('chat.steps', { count: steps }) : ''}
          </Text>
        )}
        {folded && buttons}
        <Tooltip label={folded ? t('chat.unfold') : t('chat.fold')}>
          <ActionIcon size={28} radius="xl" variant="subtle" color="gray" onClick={() => setFolded(!folded)}
            aria-label={folded ? t('chat.unfold') : t('chat.fold')} aria-expanded={!folded} data-turn-fold>
            {folded ? <IconChevronUp size={15} /> : <IconChevronDown size={15} />}
          </ActionIcon>
        </Tooltip>
      </div>
      {!folded && (
        <div className={s.log} data-turn-log>
          <div className={s.logLines} role={turn.open ? 'button' : undefined} tabIndex={turn.open ? 0 : undefined}
            aria-label={turn.open ? t('chat.full_log') : undefined} title={turn.open ? t('chat.full_log') : undefined}
            onClick={() => turn.open && void open.send()} onKeyDown={(e) => { if (turn.open && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); void open.send(); } }}>
            {lines.length ? lines.map((l) => (
              <div key={l.key} className={s.logLine} data-failed={l.failed || undefined} data-turn-line>{l.text}</div>
            )) : <div className={s.logLine} data-empty>…</div>}
          </div>
          <div className={s.logButtons}>{buttons}</div>
        </div>
      )}
      {!folded && <Processes list={turn.processes ?? []} />}
    </div>
  );
}
