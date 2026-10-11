import { Group, Stack, Text } from '@mantine/core';
import { Browser } from './Browser';
import { Download } from './Download';
import { kindOf, PREVIEW, type Item } from './kinds';
import { Picture } from './Picture';
import { Viewer } from './Viewer';

export type { Item } from './kinds';

// Media shows the files of a source: one in place, a picture as a thumbnail that opens in full; several as tiles or
// a list that open in a lightbox.
export function Media({ items }: { items: Item[] }) {
  if (items.length > 1) return <Browser items={items} />;
  return (
    <Stack gap="sm">
      {items.map((it, i) => {
        const picture = !it.error && kindOf(it) === 'image' && (it.size ?? 0) <= PREVIEW;
        return (
          <Stack key={String(it.id) + i} gap={4} data-item>
            {!picture && !it.error && (
              <Group justify="space-between"><Text size="sm" fw={500}>{it.title || it.name}</Text><Download it={it} /></Group>
            )}
            {picture ? <Picture it={it} wide /> : <Viewer it={it} />}
          </Stack>
        );
      })}
    </Stack>
  );
}
