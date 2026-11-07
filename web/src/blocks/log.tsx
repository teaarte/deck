import { useRef, useState } from 'react';
import { ActionIcon, Badge, CloseButton, Code, Group, Spoiler, Stack, Text, TextInput } from '@mantine/core';
import { IconActivity, IconAlertTriangle, IconBan, IconBulb, IconMessage, IconPaperclip, IconSend, IconTerminal2, IconTool } from '@tabler/icons-react';
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
import { locale } from '../lib/i18n';
import { useAttachments } from '../chat/useAttachments';

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
    <Spoiler maxHeight={132} showLabel={t('Show more')} hideLabel={t('Show less')}>
      <Code block {...{ [mark]: '' }} data-json={v.json || undefined} className="deck-pre">{v.text}</Code>
    </Spoiler>
  );
}

// At is the time of a line, the date only when it is not today, with the whole of it on hover.
function At({ at }: { at: string }) {
  const d = new Date(at);
  const today = d.toDateString() === new Date().toDateString();
  const text = today ? d.toLocaleTimeString(locale(), { hour: '2-digit', minute: '2-digit', second: '2-digit' })
    : d.toLocaleString(locale(), { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
  return <Text size="xs" c="dimmed" ml="auto" className="deck-fixed" title={d.toLocaleString(locale())}>{text}</Text>;
}

// Live is the head of a log still going: how long it runs and how many steps it made.
function Live({ since, steps }: { since: string; steps: number }) {
  const { t } = useTranslation();
  const now = useClock(1000);
  return (
    <Group gap="xs" className={s.live} data-log-live>
      <IconActivity size={15} color="var(--mantine-color-green-7)" aria-hidden />
      <Text size="xs" c="dimmed">
        {t('going')} · {formatted(Math.max(0, (now - Date.parse(since)) / 1000), { format: 'duration' })}{steps > 0 ? ' · ' + t('Steps: {{count}}', { count: steps }) : ''}
      </Text>
    </Group>
  );
}

// Say is the line the owner writes to the agent of a process, a message while it goes or a rework once it ended, with
// files of the project attached when it says which.
function Say({ act, project }: { act: Action; project?: string }) {
  const { t } = useTranslation();
  const [text, setText] = useState('');
  const { busy, send, error, clear } = usePress(act);
  const files = useAttachments(() => undefined, project);
  const pick = useRef<HTMLInputElement>(null);
  const [failed, setFailed] = useState('');
  // A rework may go with nothing: a process that failed goes on where it stopped.
  const go = async () => {
    if (!text.trim() && !files.files.length && act.verb !== 'rework') return;
    let ids: string[] | undefined;
    try {
      ids = await files.ids();
    } catch (e) {
      setFailed((e as Error).message);
      return;
    }
    void send({ [act.input ?? 'text']: text, ...(ids?.length ? { files: ids } : {}) }).then((r) => {
      if (r && !r.error) {
        setText('');
        files.clear();
      }
    });
  };
  return (
    <Failed error={error || failed} onClose={() => { clear(); setFailed(''); }}>
      <Stack gap={6} data-log-say-box>
        <TextInput size="sm" value={text} onChange={(e) => setText(e.currentTarget.value)} placeholder={t(act.verb === 'rework' ? 'What to change' : 'Write to the agent')} data-log-say
          onKeyDown={(e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); void go(); } }}
          leftSection={project ? <ActionIcon variant="subtle" color="gray" onClick={() => pick.current?.click()} aria-label={t('Attach files')} data-log-attach><IconPaperclip size={15} /></ActionIcon> : undefined}
          rightSection={<ActionIcon variant="subtle" loading={busy} onClick={() => void go()} aria-label={t('Send')}><IconSend size={15} /></ActionIcon>} />
        {project && <input ref={pick} type="file" multiple hidden onChange={(e) => { files.attach(Array.from(e.currentTarget.files ?? [])); e.currentTarget.value = ''; }} />}
        {files.files.length > 0 && (
          <Group gap={6} data-log-files>
            {files.files.map((f, i) => (
              <Badge key={i} variant="light" color="gray" tt="none" rightSection={<CloseButton size="xs" onClick={() => files.remove(i)} aria-label={t('Remove')} />}>{f.name}</Badge>
            ))}
          </Group>
        )}
      </Stack>
    </Failed>
  );
}

// Owner is a message of the owner among the lines, with whether the agent took it and, while not, its delete.
// STATUS and KINDS are the words of the state of a message of the owner and of the kind of a line.
const STATUS: Record<string, string> = { waiting: 'not taken yet', taken: 'taken', running: 'going' };
// MARKS are the icons of the kinds of lines.
const MARKS: Record<string, typeof IconTool> = { message: IconMessage, thought: IconBulb, tool: IconTool, stderr: IconTerminal2, denied: IconBan, warning: IconAlertTriangle };
const KINDS: Record<string, string> = { message: 'Agent', thought: 'Thinking', tool: 'Tool', rate_limit: 'Limit', stderr: 'stderr', denied: 'Refused', owner: 'You', deck: 'deck' };

function Owner({ l }: { l: Line }) {
  const { t } = useTranslation();
  return (
    <Stack gap={4} align="flex-end" data-line="owner" data-status={l.status}>
      <div className={s.owner}><Text size="sm" className="deck-pre">{l.text}</Text></div>
      <Group gap={6}>
        <Text size="xs" c="dimmed">{t(STATUS[l.status ?? 'taken'] ?? l.status ?? '')}</Text>
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
      {d.say && <Say act={d.say} project={d.attach} />}
      {lines.filter((l) => l.kind !== 'tool_update' || !l.id).map((l, i) => {
        if (l.kind === 'owner') return <Owner key={i} l={l} />;
        const end = l.kind === 'tool' && l.id ? done.get(l.id) : undefined;
        const Mark = MARKS[l.kind];
        return (
          <Stack key={i} gap={4} miw={0} data-line={l.kind}>
            <Group gap="xs" wrap="nowrap" miw={0}>
              <Badge size="xs" variant="light" color={end?.status === 'failed' || l.kind === 'denied' ? 'red' : 'gray'} className="deck-fixed"
                leftSection={Mark ? <Mark size={11} stroke={2} /> : undefined}>{t(KINDS[l.kind] ?? l.kind)}</Badge>
              {l.title && <Text size="xs" ff="monospace" truncate>{l.title}</Text>}
              <At at={l.at} />
            </Group>
            {l.text && (l.kind === 'message' ? <Spoiler maxHeight={260} showLabel={t('Show more')} hideLabel={t('Show less')}><Markdown text={l.text} /></Spoiler>
              : l.kind === 'thought' ? <Spoiler maxHeight={72} showLabel={t('Show more')} hideLabel={t('Show less')}><Text size="sm" c="dimmed" fs="italic" className="deck-pre">{l.text}</Text></Spoiler>
              : <Raw text={l.text} mark="data-tool-input" />)}
            {end?.text && <Raw text={end.text} mark="data-tool-result" />}
          </Stack>
        );
      })}
    </Stack>
  );
}

export const log = blockImpl('log', (_, d) => <LogLines d={d} />);
