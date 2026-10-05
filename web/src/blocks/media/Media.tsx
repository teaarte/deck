import { Group, SimpleGrid, Stack, Text } from '@mantine/core';
import { formatted } from '../../lib/format';
import type { Data } from '../../lib/catalog.gen';
import { Download } from './Download';
import { Pdf } from './Pdf';
import { Player } from './Player';
import { Picture } from './Picture';

export type Item = NonNullable<Data['files']>[number];

// PICTURES are the types drawn as a picture: raster only, so no file runs as a script of deck.
const PICTURES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/avif', 'image/bmp'];
// PREVIEW is the largest file read for a preview; a larger one is only a download.
const PREVIEW = 20 * 1000 * 1000;
const shows = (it: Item, types: string[]) => !it.error && types.includes(it.type ?? '') && (it.size ?? 0) <= PREVIEW;
const isPicture = (it: Item) => shows(it, PICTURES);
const isPdf = (it: Item) => shows(it, ['application/pdf']);
// isPlayed is a video or a sound, which plays from its address, so any size.
const isPlayed = (it: Item) => !it.error && /^(video|audio)\//.test(it.type ?? '');

// Media shows the files of a source: a picture as a thumbnail that opens in full, a PDF in place, a video or a sound
// in a player, the rest by name and size with a download.
export function Media({ items }: { items: Item[] }) {
  return (
    <Stack gap="sm">
      <SimpleGrid cols={{ base: 2, sm: 3, md: 4 }} spacing="sm">
        {items.filter(isPicture).map((it) => (
          <Stack key={String(it.id)} gap={4} data-item><Picture it={it} /><Text size="xs" truncate>{it.title || it.name}</Text></Stack>
        ))}
      </SimpleGrid>
      {items.filter(isPlayed).map((it, i) => <Player key={String(it.id)} it={it} first={i === 0} />)}
      {items.filter(isPdf).map((it) => (
        <Stack key={String(it.id)} gap={4} data-item>
          <Group justify="space-between"><Text size="sm" fw={500}>{it.title || it.name}</Text><Download it={it} /></Group>
          <Pdf it={it} />
        </Stack>
      ))}
      {items.filter((it) => !isPicture(it) && !isPdf(it) && !isPlayed(it)).map((it) => (
        <Group key={String(it.id)} justify="space-between" wrap="nowrap" data-item data-media={it.error ? 'error' : 'file'}>
          <Stack gap={0} miw={0}>
            <Text size="sm" fw={500} truncate>{it.title || it.name}</Text>
            {it.error ? <Text size="xs" c="red">{it.error}</Text>
              : <Text size="xs" c="dimmed">{[it.name !== it.title && it.title ? it.name : '', formatted(it.size, { format: 'bytes' })].filter(Boolean).join(' · ')}</Text>}
          </Stack>
          {!it.error && <Download it={it} />}
        </Group>
      ))}
    </Stack>
  );
}
