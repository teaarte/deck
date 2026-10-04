import { Group, Stack, Text } from '@mantine/core';
import type { CardValue } from '../lib/catalog.gen';
import { Actions } from '../ui/Actions';
import { Markdown } from '../ui/Markdown';
import { Value } from '../ui/Value';
import { blockImpl } from './blockImpl';

// card draws the fields of one record, label by value, and its buttons.
export const card = blockImpl('card', (_, d) => {
  const v = (d.value ?? {}) as Partial<CardValue>;
  return (
    <Stack gap="xs">
      {(v.fields ?? []).map((f) => (
        f.markdown && typeof f.value === 'string' ? (
          <Stack key={f.label} gap={2}>
            <Text size="sm" c="dimmed">{f.label}</Text>
            <Markdown text={f.value} />
          </Stack>
        ) : (
          <Group key={f.label} gap="sm" wrap="nowrap" align="flex-start">
            <Text size="sm" c="dimmed" w={120}>{f.label}</Text>
            <Text size="sm" component="div"><Value v={f.value} s={f} tone={f.tone} /></Text>
          </Group>
        )
      ))}
      <Actions actions={v.actions} />
    </Stack>
  );
});
