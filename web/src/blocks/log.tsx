import { Badge, Code, Group, Spoiler, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { Data } from '../lib/catalog.gen';
import { Markdown } from '../ui/Markdown';
import { blockImpl } from './blockImpl';

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

// LogLines are the lines of a run: messages in markdown, thoughts and tools, each tool with how it ended.
function LogLines({ lines }: { lines: NonNullable<Data['lines']> }) {
  const { t } = useTranslation();
  const done = new Map(lines.filter((l) => l.kind === 'tool_update' && l.id).map((l) => [l.id, l]));
  return (
    <Stack gap="sm">
      {lines.filter((l) => l.kind !== 'tool_update' || !l.id).map((l, i) => {
        const end = l.kind === 'tool' && l.id ? done.get(l.id) : undefined;
        return (
          <Stack key={i} gap={4} miw={0} data-line={l.kind}>
            <Group gap="xs" wrap="nowrap" miw={0}>
              <Badge size="xs" variant="light" color={end?.status === 'failed' ? 'red' : 'gray'} style={{ flexShrink: 0 }}>{t('log.' + l.kind, { defaultValue: l.kind })}</Badge>
              {l.title && <Text size="xs" ff="monospace" truncate>{l.title}</Text>}
              <Text size="xs" c="dimmed" ml="auto" style={{ flexShrink: 0 }}>{new Date(l.at).toLocaleTimeString()}</Text>
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

export const log = blockImpl('log', (_, d) => <LogLines lines={d.lines ?? []} />);
