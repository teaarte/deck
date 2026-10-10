import { useState } from 'react';
import { Group, SegmentedControl, Stack, Text, UnstyledButton } from '@mantine/core';
import { useLocalStorage } from '@mantine/hooks';
import { IconLayoutGrid, IconList } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { formatted } from '../../lib/format';
import { Actions } from '../../ui/actions/Actions';
import type { Item } from './kinds';
import { Lightbox } from './Lightbox';
import { Thumb } from './Thumb';
import s from './files.module.css';

type View = { mode: 'grid' | 'list'; size: 's' | 'm' | 'l' };

// TILES is the side of the picture of a tile of each size.
const TILES = { s: 72, m: 120, l: 180 };

// Browser shows many files as tiles or as a list, kept per device, and opens each in a lightbox.
export function Browser({ items }: { items: Item[] }) {
  const { t } = useTranslation();
  const [view, setView] = useLocalStorage<View>({ key: 'deck-files-view', defaultValue: { mode: 'grid', size: 'm' } });
  const [at, setAt] = useState<number | null>(null);
  const tile = TILES[view.size] ?? TILES.m;
  return (
    <Stack gap="sm" data-files={view.mode}>
      <Group gap="xs" justify="flex-end">
        {view.mode === 'grid' && (
          <SegmentedControl size="xs" value={view.size} onChange={(v) => setView({ ...view, size: v as View['size'] })} data-files-size
            data={['s', 'm', 'l'].map((v) => ({ value: v, label: v.toUpperCase() }))} />
        )}
        <SegmentedControl size="xs" value={view.mode} onChange={(v) => setView({ ...view, mode: v as View['mode'] })} data-files-mode
          data={[{ value: 'grid', label: <IconLayoutGrid size={14} aria-label={t('files.grid')} /> }, { value: 'list', label: <IconList size={14} aria-label={t('files.list')} /> }]} />
      </Group>
      {view.mode === 'grid' ? (
        <div className={s.grid} style={{ gridTemplateColumns: `repeat(auto-fill, minmax(${tile + 16}px, 1fr))` }}>
          {items.map((it, i) => (
            <UnstyledButton key={String(it.id) + i} className={s.tile} onClick={() => setAt(i)} data-item data-tile>
              <Thumb it={it} size={tile} />
              <Text size="xs" lineClamp={2} ta="center" style={{ overflowWrap: 'anywhere' }}>{it.title || it.name}</Text>
            </UnstyledButton>
          ))}
        </div>
      ) : (
        <Stack gap={2}>
          {items.map((it, i) => (
            <Group key={String(it.id) + i} gap={6} wrap="nowrap" data-item data-row>
              <UnstyledButton className={s.row} style={{ flex: 1, minWidth: 0 }} onClick={() => setAt(i)}>
                <Thumb it={it} size={32} />
                <Text size="sm" truncate style={{ flex: 1 }}>{it.title || it.name}</Text>
                {it.error ? <Text size="xs" c="red" truncate maw="40%">{it.error}</Text>
                  : <Text size="xs" c="dimmed">{formatted(it.size, { format: 'bytes' })}</Text>}
              </UnstyledButton>
              {!!it.actions?.length && <Actions actions={it.actions} max={2} />}
            </Group>
          ))}
        </Stack>
      )}
      <Lightbox items={items} at={at} onAt={setAt} />
    </Stack>
  );
}
