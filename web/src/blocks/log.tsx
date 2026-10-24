import { useState } from 'react';
import { ActionIcon, Badge, Code, Group, Spoiler, Stack, Text, TextInput } from '@mantine/core';
import { IconActivity, IconSend } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { Action, Data } from '../lib/catalog.gen';
import { formatted } from '../lib/format';
import { useClock } from '../hooks/useClock';
import { ActionButton } from '../ui/actions/ActionButton';
import { Failed } from '../ui/actions/Failed';
import { usePress } from '../ui/actions/usePress';
import { Markdown } from '../ui/Markdown';
import { blockImpl } from './blockImpl';
import s from './log.module.css';

type Line = NonNullable<Data['lines']>[number];

// CAP is how much of a text of a tool the log shows.
const CAP = 20000;

// laidOut is a text of JSON with its keys on lines of their own, any other text as it is, cut at CAP.
function laidOut(text: string): { text: string; json: boolean } {
  if (text.length > CAP) return { text: text.slice(0, CAP) + '…', json: false };
  if (!/^\s*[[{]/.test(text)) return { text, json: false };
  try {
    return { text: JSON.stringify(JSON.parse(text), null, 2), json: true };
  } catch {
    return { text, json: false };
  }
}

// Raw is a text of a tool in a monospace block that wraps its long lines, folded past a few lines.
function Raw({ text, mark }: { text: string; mark: string }) {
  const { t } = useTranslation();
  const v = laidOut(text);
  return (
    <Spoiler maxHeight={132} showLabel={t('log.more')} hideLabel={t('log.less')}>
      <Code block {...{ [mark]: '' }} data-json={v.json || undefined} style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{v.text}</Code>
    </Spoiler>
  );
}

// At is the time of a line, the date only when it is not today, with the whole of it on hover.
function At({ at }: { at: string }) {
  const d = new Date(at);
  const today = d.toDateString() === new Date().toDateString();
  const text = today ? d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
    : d.toLocaleString([], { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
  return <Text size="xs" c="dimmed" ml="auto" style={{ flexShrink: 0 }} title={d.toLocaleString()}>{text}</Text>;
}

// Live is the head of a log still going: how long it runs and how many steps it made.
function Live({ since, steps }: { since: string; steps: number }) {
  const { t } = useTranslation();
  const now = useClock(1000);
  return (
    <Group gap="xs" className={s.live} data-log-live>
      <IconActivity size={15} color="var(--mantine-color-green-7)" aria-hidden />
      <Text size="xs" c="dimmed">
        {t('log.running')} · {formatted(Math.max(0, (now - Date.parse(since)) / 1000), { format: 'duration' })}{steps > 0 ? ' · ' + t('chat.steps', { count: steps }) : ''}
      </Text>
    </Group>
  );
}

// Say is the line the owner writes to the agent of a process still going.
function Say({ act }: { act: Action }) {
  const { t } = useTranslation();
  const [text, setText] = useState('');
  const { busy, send, error, clear } = usePress(act);
  const go = () => {
    if (!text.trim()) return;
    void send({ [act.input ?? 'text']: text }).then((r) => { if (r && !r.error) setText(''); });
  };
  return (
    <Failed error={error} onClose={clear}>
      <TextInput size="sm" value={text} onChange={(e) => setText(e.currentTarget.value)} placeholder={t('log.write')} data-log-say
        onKeyDown={(e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); go(); } }}
        rightSection={<ActionIcon variant="subtle" loading={busy} onClick={go} aria-label={t('verb.send')}><IconSend size={15} /></ActionIcon>} />
    </Failed>
  );
}

// Owner is a message of the owner among the lines, with whether the agent took it and, while not, its delete.
function Owner({ l }: { l: Line }) {
  const { t } = useTranslation();
  return (
    <Stack gap={4} align="flex-end" data-line="owner" data-status={l.status}>
      <div className={s.owner}><Text size="sm" style={{ whiteSpace: 'pre-wrap' }}>{l.text}</Text></div>
      <Group gap={6}>
        <Text size="xs" c="dimmed">{t('log.' + (l.status ?? 'taken'))}</Text>
        <At at={l.at} />
        {l.delete && <ActionButton act={l.delete} />}
      </Group>
    </Stack>
  );
}

// LogLines are the lines of a run, newest first: messages in markdown, thoughts, tools, each tool with how it ended,
// and the owner's messages.
function LogLines({ d }: { d: Data }) {
  const { t } = useTranslation();
  const lines = d.lines ?? [];
  const done = new Map(lines.filter((l) => l.kind === 'tool_update' && l.id).map((l) => [l.id, l]));
  return (
    <Stack gap="sm">
      {d.since && <Live since={d.since} steps={lines.filter((l) => l.kind === 'tool').length} />}
      {d.say && <Say act={d.say} />}
      {lines.filter((l) => l.kind !== 'tool_update' || !l.id).map((l, i) => {
        if (l.kind === 'owner') return <Owner key={i} l={l} />;
        const end = l.kind === 'tool' && l.id ? done.get(l.id) : undefined;
        return (
          <Stack key={i} gap={4} miw={0} data-line={l.kind}>
            <Group gap="xs" wrap="nowrap" miw={0}>
              <Badge size="xs" variant="light" color={end?.status === 'failed' || l.kind === 'denied' ? 'red' : 'gray'} style={{ flexShrink: 0 }}>{t('log.' + l.kind, { defaultValue: l.kind })}</Badge>
              {l.title && <Text size="xs" ff="monospace" truncate>{l.title}</Text>}
              <At at={l.at} />
            </Group>
            {l.text && (l.kind === 'message' ? <Markdown text={l.text} />
              : l.kind === 'thought' ? <Text size="sm" c="dimmed" fs="italic" lineClamp={4} style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{l.text}</Text>
              : <Raw text={l.text} mark="data-tool-input" />)}
            {end?.text && <Raw text={end.text} mark="data-tool-result" />}
          </Stack>
        );
      })}
    </Stack>
  );
}

export const log = blockImpl('log', (_, d) => <LogLines d={d} />);
