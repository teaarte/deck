import { Group, Stack, Text } from '@mantine/core';
import { Browser } from './Browser';
import { Download } from './Download';
import { kindOf, PREVIEW, type Item } from './kinds';
import { Picture } from './Picture';
import { Viewer } from './Viewer';

export type { Item } from './kinds';

// marked is a player with marks or a mark button, which stays in place to be marked.
const marked = (it: Item) => ['video', 'audio'].includes(kindOf(it)) && (!!it.mark || (it.marks?.length ?? 0) > 0);

// Media shows the files of a source: one in place, a picture as a thumbnail that opens in full, a player with its
// marks in place too; several as tiles or a list that open in a lightbox.
export function Media({ items }: { items: Item[] }) {
  if (items.length > 1 && !items.some(marked)) return <Browser items={items} />;
  return (
    <Stack gap="sm">
      {items.map((it, i) => {
        const picture = !it.error && kindOf(it) === 'image' && (it.size ?? 0) <= PREVIEW;
        return (
          <Stack key={String(it.id) + i} gap={4} data-item>
            {!picture && kindOf(it) !== 'video' && kindOf(it) !== 'audio' && !it.error && (
              <Group justify="space-between"><Text size="sm" fw={500}>{it.title || it.name}</Text><Download it={it} /></Group>
            )}
            {picture ? <Picture it={it} wide /> : <Viewer it={it} first={i === 0} />}
          </Stack>
        );
      })}
    </Stack>
  );
}
