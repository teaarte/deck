import { ActionIcon, Text, Tooltip } from '@mantine/core';
import { useLocalStorage } from '@mantine/hooks';
import { IconArrowUpRight, IconChevronDown, IconChevronUp, IconPlayerStopFilled } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { ChatValue } from '../lib/catalog.gen';
import { ActionIconButton } from '../ui/ActionIconButton';
import a from '../ui/aura.module.css';
import { Glyph } from '../ui/Glyph';
import { useActionRun } from '../ui/useActionRun';
import s from './chat.module.css';

type TurnValue = NonNullable<ChatValue['turn']>;

const SHOWN = 6;

// logLines are the calls and messages of the log as lines, a call marked by how it ended.
function logLines(lines: TurnValue['lines']): { key: string; text: string; failed?: boolean }[] {
  const ended = new Map(lines.filter((l) => l.kind === 'tool_update' && l.id).map((l) => [l.id!, l.status]));
  return lines.filter((l) => l.kind !== 'tool_update').map((l, i) => {
    if (l.kind !== 'tool') return { key: String(i), text: (l.text ?? '').split('\n').find((x) => x.trim()) ?? '' };
    const status = l.id ? ended.get(l.id) : undefined;
    return { key: String(i), text: `${status === 'failed' ? '✗' : status ? '✓' : '›'} ${l.title ?? ''}`, failed: status === 'failed' };
  }).filter((l) => l.text.trim()).slice(-SHOWN);
}

// Turn is the reply going on: what it does or waits for over a soft drift, the tail of its log that opens the whole,
// and its buttons; folded, only the line that says what it does.
export function Turn({ turn }: { turn: TurnValue }) {
  const { t } = useTranslation();
  const [folded, setFolded] = useLocalStorage({ key: 'deck-chat-turn-folded', defaultValue: false });
  const open = useActionRun(turn.open ?? {});
  const running = turn.state === 'running';
  const lines = logLines(turn.lines);
  const buttons = (
    <>
      {turn.stop && <ActionIconButton act={turn.stop} color="red" variant="light" icon={<IconPlayerStopFilled size={13} />} />}
      {turn.open && <ActionIconButton act={turn.open} variant="default" icon={<IconArrowUpRight size={15} />} />}
    </>
  );
  return (
    <div className={s.turn} data-turn={turn.state} data-folded={folded || undefined}>
      <div className={`${s.turnHead} ${a.aura}`} data-live={running || undefined}>
        <span className={s.turnMark}><Glyph busy={running} /></span>
        <Text size="xs" fw={500} truncate style={{ flex: 1 }}>
          {running ? t('chat.replying', { provider: turn.provider ?? '' }) : t('chat.waits.' + turn.state, { defaultValue: turn.state })}
        </Text>
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
    </div>
  );
}
