import { useState } from 'react';
import { Button, Group, Text, UnstyledButton } from '@mantine/core';
import { IconArrowUpRight, IconCheck, IconChevronDown, IconChevronUp } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { Action } from '../lib/catalog.gen';
import type { ChatData } from './chatData';
import { useClock } from '../hooks/useClock';
import { toneColor } from '../lib/format';
import { ActionIconButton } from '../ui/actions/ActionIconButton';
import { Deki, DekiHead, turnPose, useMascot } from '../ui/deki/Deki';
import { Glyph } from '../ui/Glyph';
import { Logo } from '../ui/logo/Logo';
import { usePress } from '../ui/actions/usePress';
import s from './chat.module.css';

type TurnValue = NonNullable<ChatData['turn']>;
type Proc = NonNullable<TurnValue['processes']>[number];

const SHOWN = 6;

const firstLine = (text?: string) => (text ?? '').split('\n').find((x) => x.trim())?.trim() ?? '';

// logLines are the calls of the log as lines, each marked by how it ended.
function logLines(lines: TurnValue['lines'], shown = SHOWN): { key: string; text: string; failed?: boolean }[] {
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
          <Text size="xs" ff="monospace" truncate className="deck-grow">{p.project}.{p.command}</Text>
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
  return <Button size="compact-xs" color="red" variant="light" loading={busy} onClick={() => void send()} data-verb="stop">{t('Stop')}</Button>;
}

// Turn is the reply going on in one line: what it does now with a timer, the model and Stop; a press on the line
// opens the steps it said, its raw calls and the processes it started.
export function Turn({ turn }: { turn: TurnValue }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const log = usePress(turn.open ?? {});
  const running = turn.state === 'running';
  const mascot = useMascot();
  const steps = said(turn.lines);
  const calls = logLines(turn.lines);
  const procs = turn.processes ?? [];
  const tick = useClock(1000, running);
  const started = turn.started_at ? Date.parse(turn.started_at) : NaN;
  const now = calling(turn.lines);
  const head = turn.waits || !running ? turn.word : now?.doing ?? steps[steps.length - 1] ?? t(mascot ? 'Deki is working' : 'The master is working');
  const more = steps.length > 0 || calls.length > 0 || procs.length > 0;
  return (
    <div className={s.turn} data-turn={turn.state}>
      <div className={s.turnHead} data-turn-head data-live={running || undefined} data-open={open || undefined}>
        <UnstyledButton className={s.turnFold} disabled={!more} onClick={() => setOpen(!open)} aria-expanded={more ? open : undefined} data-turn-fold={more || undefined}>
          <span className={s.turnMark}>
            {!mascot ? <Glyph busy={running} /> : running || turn.waits ? <Deki pose={turnPose(!!turn.waits, now?.act)} size={26} />
              : <DekiHead size={24} />}
          </span>
          <Text size="sm" fw={600} truncate className="deck-grow" c={running && !turn.waits ? undefined : toneColor(turn.tone)} data-turn-now>{head}</Text>
          {running && !Number.isNaN(started) && <Text size="xs" c="dimmed" className={s.turnFact} data-turn-timer>{clock(Math.max(0, (tick - started) / 1000))}</Text>}
          {turn.provider && (
            <span className={s.turnModel} title={t('The model of this turn')} data-turn-model>
              <Logo names={turn.provider} size={14} /><Text span size="xs" c="dimmed" truncate>{turn.provider}</Text>
            </span>
          )}
          {more && <span className={s.turnChevron} aria-hidden>{open ? <IconChevronUp size={14} /> : <IconChevronDown size={14} />}</span>}
        </UnstyledButton>
        {turn.stop && <Stop act={turn.stop} />}
      </div>
      {open && (
        <div className={s.turnBody} data-turn-open>
          {steps.slice(-SHOWN).map((x, i, all) => {
            const last = i === all.length - 1;
            return (
              <div key={i} className={s.step} data-now={last || undefined} data-turn-step>
                <span className={s.stepMark}>{last ? '•' : <IconCheck size={11} />}</span>
                <span>{x}</span>
              </div>
            );
          })}
          {calls.length > 0 && (
            <div className={s.log} data-turn-log>
              <div className={s.logLines} role={turn.open ? 'button' : undefined} tabIndex={turn.open ? 0 : undefined}
                aria-label={turn.open ? t('Open the whole log') : undefined} title={turn.open ? t('Open the whole log') : undefined}
                onClick={() => turn.open && void log.send()} onKeyDown={(e) => { if (turn.open && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); void log.send(); } }}>
                {calls.map((l) => <div key={l.key} className={s.logLine} data-failed={l.failed || undefined} data-turn-line>{l.text}</div>)}
              </div>
              {turn.open && <div className={s.logButtons}><ActionIconButton act={turn.open} variant="default" icon={<IconArrowUpRight size={15} />} /></div>}
            </div>
          )}
          <Processes list={procs} />
        </div>
      )}
    </div>
  );
}
