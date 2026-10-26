import { useState } from 'react';
import { Button, Group, Text } from '@mantine/core';
import { IconArrowUpRight, IconCheck, IconChevronDown, IconChevronUp } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { Action } from '../lib/catalog.gen';
import type { ChatData } from './chatData';
import { useClock } from '../hooks/useClock';
import { toneColor } from '../lib/format';
import { ActionIconButton } from '../ui/actions/ActionIconButton';
import { Deki, DekiHead, turnPose, useMascot } from '../ui/deki/Deki';
import { Glyph } from '../ui/Glyph';
import { usePress } from '../ui/actions/usePress';
import s from './chat.module.css';

type TurnValue = NonNullable<ChatData['turn']>;
type Proc = NonNullable<TurnValue['processes']>[number];

const SHOWN = 6;

const firstLine = (text?: string) => (text ?? '').split('\n').find((x) => x.trim())?.trim() ?? '';

// logLines are the calls of the log as lines, each marked by how it ended.
export function logLines(lines: TurnValue['lines'], shown = SHOWN): { key: string; text: string; failed?: boolean }[] {
  const ended = new Map(lines.filter((l) => l.kind === 'tool_update' && l.id).map((l) => [l.id!, l.status]));
  return lines.map((l, i) => ({ l, i })).filter(({ l }) => l.kind === 'tool').map(({ l, i }) => {
    const status = l.id ? ended.get(l.id) : undefined;
    return { key: String(i), text: `${status === 'failed' ? '✗' : status ? '✓' : '›'} ${l.title ?? ''}`, failed: status === 'failed' };
  }).filter((l) => l.text.trim()).slice(-shown);
}

// calling is the call of the log still going, which says what the turn does now; none while it thinks or speaks.
function calling(lines: TurnValue['lines']): TurnValue['lines'][number] | undefined {
  const ended = new Set(lines.filter((l) => l.kind === 'tool_update' && l.id && l.status).map((l) => l.id));
  const last = [...lines].reverse().find((l) => l.kind === 'tool' || l.kind === 'message');
  return last?.kind === 'tool' && !(last.id && ended.has(last.id)) ? last : undefined;
}

// said are the first lines of what the agent said on the way, its steps in words.
const said = (lines: TurnValue['lines']) => lines.filter((l) => l.kind === 'message').map((l) => firstLine(l.text)).filter(Boolean);

// clock is seconds as m:ss.
const clock = (secs: number) => `${Math.floor(secs / 60)}:${String(Math.floor(secs % 60)).padStart(2, '0')}`;

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

// Stop is the red word that stops the reply.
function Stop({ act }: { act: Action }) {
  const { t } = useTranslation();
  const { busy, send } = usePress(act);
  return <Button size="compact-xs" color="red" variant="light" loading={busy} onClick={() => void send()} data-verb="stop">{t('chat.stop')}</Button>;
}

// Turn is the reply going on: what it does now in its own words with a timer and Stop, the steps it said before,
// and its raw calls folded under Log.
export function Turn({ turn }: { turn: TurnValue }) {
  const { t } = useTranslation();
  const [log, setLog] = useState(false);
  const open = usePress(turn.open ?? {});
  const running = turn.state === 'running';
  const mascot = useMascot();
  const steps = said(turn.lines);
  const calls = logLines(turn.lines);
  const tick = useClock(1000, running);
  const started = turn.started_at ? Date.parse(turn.started_at) : NaN;
  const now = calling(turn.lines);
  const head = turn.waits || !running ? turn.word : now?.doing ?? steps[steps.length - 1] ?? t(mascot ? 'chat.working_deki' : 'chat.working_master');
  return (
    <div className={s.turn} data-turn={turn.state}>
      <div className={s.turnHead} data-turn-head data-live={running || undefined}>
        <span className={s.turnMark}>
          {!mascot ? <Glyph busy={running} /> : running || turn.waits ? <Deki pose={turnPose(!!turn.waits, now?.act)} size={26} />
            : <DekiHead size={24} />}
        </span>
        <Text size="sm" fw={600} truncate style={{ flex: 1 }} c={running && !turn.waits ? undefined : toneColor(turn.tone)} data-turn-now>{head}</Text>
        {running && !Number.isNaN(started) && <Text size="xs" c="dimmed" data-turn-timer>{clock(Math.max(0, (tick - started) / 1000))}</Text>}
        {turn.stop && <Stop act={turn.stop} />}
      </div>
      {steps.length > 1 && (
        <div className={s.steps} data-turn-steps>
          {steps.slice(-SHOWN).map((x, i, all) => {
            const last = i === all.length - 1;
            return (
              <div key={i} className={s.step} data-now={last || undefined} data-turn-step>
                <span className={s.stepMark}>{last ? '•' : <IconCheck size={11} />}</span>
                <span>{x}</span>
              </div>
            );
          })}
        </div>
      )}
      {calls.length > 0 && (
        <Button size="compact-xs" variant="subtle" color="gray" className={s.logToggle} onClick={() => setLog(!log)}
          rightSection={log ? <IconChevronUp size={12} /> : <IconChevronDown size={12} />} aria-expanded={log} data-turn-fold>
          {log ? t('chat.hide_log') : t('chat.log')}
        </Button>
      )}
      {log && (
        <div className={s.log} data-turn-log>
          <div className={s.logLines} role={turn.open ? 'button' : undefined} tabIndex={turn.open ? 0 : undefined}
            aria-label={turn.open ? t('chat.full_log') : undefined} title={turn.open ? t('chat.full_log') : undefined}
            onClick={() => turn.open && void open.send()} onKeyDown={(e) => { if (turn.open && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); void open.send(); } }}>
            {calls.map((l) => <div key={l.key} className={s.logLine} data-failed={l.failed || undefined} data-turn-line>{l.text}</div>)}
          </div>
          {turn.open && <div className={s.logButtons}><ActionIconButton act={turn.open} variant="default" icon={<IconArrowUpRight size={15} />} /></div>}
        </div>
      )}
      <Processes list={turn.processes ?? []} />
    </div>
  );
}
