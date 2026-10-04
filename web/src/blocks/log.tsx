import { Badge, Code, Group, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { Data } from '../lib/catalog.gen';
import { blockImpl } from './blockImpl';

// LogLines are the lines of a run: messages, thoughts and tools, each tool with how it ended.
function LogLines({ lines }: { lines: NonNullable<Data['lines']> }) {
  const { t } = useTranslation();
  const done = new Map(lines.filter((l) => l.kind === 'tool_update' && l.id).map((l) => [l.id, l]));
  return (
    <Stack gap={6}>
      {lines.filter((l) => l.kind !== 'tool_update' || !l.id).map((l, i) => {
        const end = l.kind === 'tool' && l.id ? done.get(l.id) : undefined;
        return (
          <Stack key={i} gap={2} data-line={l.kind}>
            <Group gap="xs">
              <Badge size="xs" variant="light" color={end?.status === 'failed' ? 'red' : 'gray'}>{t('log.' + l.kind, { defaultValue: l.kind })}</Badge>
              {l.title && <Text size="xs" ff="monospace">{l.title}</Text>}
              <Text size="xs" c="dimmed">{new Date(l.at).toLocaleTimeString()}</Text>
            </Group>
            {l.text && <Text size="sm" c={l.kind === 'thought' ? 'dimmed' : undefined} style={{ whiteSpace: 'pre-wrap' }} lineClamp={l.kind === 'message' ? undefined : 4}>{l.text}</Text>}
            {end?.text && <Code block>{end.text.slice(0, 1000)}</Code>}
          </Stack>
        );
      })}
    </Stack>
  );
}

export const log = blockImpl('log', (_, d) => <LogLines lines={d.lines ?? []} />);
