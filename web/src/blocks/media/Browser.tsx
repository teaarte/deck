import { useState } from 'react';
import { Group, SegmentedControl, Stack, Text, UnstyledButton } from '@mantine/core';
import { useLocalStorage } from '@mantine/hooks';
import { IconLayoutGrid, IconList } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { formatted } from '../../lib/format';
import { Crumbs, type Crumb } from './Crumbs';
import { Download } from './Download';
import { Actions } from '../../ui/actions/Actions';
import type { Item } from './kinds';
import { Lightbox } from './Lightbox';
import { Place } from './Place';
import { Thumb } from './Thumb';
import { Virtual } from '../../ui/Virtual';
import s from './files.module.css';

type View = { mode: 'grid' | 'list' };

// TILE is the side of the picture of a tile.
const TILE = 120;

// Browser shows many files as tiles or as a list, kept per device, and opens each in a lightbox; a line that is no
// file, such as a folder, opens by its button; crumbs are the steps of the folder it shows.
export function Browser({ items, crumbs, empty }: { items: Item[]; crumbs?: Crumb[]; empty?: string }) {
  const { t } = useTranslation();
  const [view, setView] = useLocalStorage<View>({ key: 'deck-files-view', defaultValue: { mode: 'grid' } });
  const [at, setAt] = useState<number | null>(null);
  const files = items.filter((it) => !it.icon);
  const open = (it: Item) => setAt(files.indexOf(it));
  // A line under the names makes the tiles taller.
  const tile = TILE + (items.some((it) => it.meta) ? 66 : 48);
  return (
    <Stack gap="sm" data-files={view.mode}>
      <Group gap="xs" justify="flex-end" wrap="nowrap">
        {crumbs && <Crumbs crumbs={crumbs} />}
        <SegmentedControl size="xs" value={view.mode} onChange={(v) => setView({ mode: v as View['mode'] })} data-files-mode
          data={[{ value: 'grid', label: <IconLayoutGrid size={14} aria-label={t('files.grid')} /> }, { value: 'list', label: <IconList size={14} aria-label={t('files.list')} /> }]} />
      </Group>
      {!items.length && <Text size="sm" c="dimmed" data-empty>{empty || t('state.empty')}</Text>}
      {view.mode === 'grid' ? (
        <Virtual items={items} row={tile} width={TILE + 16} className={s.grid} render={(it, i) => it.icon ? <Place key={String(it.id) + i} it={it} size={TILE} /> : (
          <UnstyledButton key={String(it.id) + i} className={s.tile} onClick={() => open(it)} data-item data-tile>
            <Thumb it={it} size={TILE} />
            <Text size="xs" lineClamp={2} ta="center" style={{ overflowWrap: 'anywhere' }}>{it.title || it.name}</Text>
            {it.error && <Text size="xs" c="red" lineClamp={2} ta="center" data-media="error">{it.error}</Text>}
          </UnstyledButton>
        )} />
      ) : (
        <Virtual items={items} row={40} gap={2} style={{ display: 'flex', flexDirection: 'column', gap: 2 }} render={(it, i) => (
            <Group key={String(it.id) + i} gap={6} wrap="nowrap" data-item data-row>
              {it.icon ? <Place it={it} size={32} row /> : <UnstyledButton className={s.row} style={{ flex: 1, minWidth: 0 }} onClick={() => open(it)}>
                <Thumb it={it} size={32} />
                <Text size="sm" truncate style={{ flex: 1 }}>{it.title || it.name}</Text>
                {it.error ? <Text size="xs" c="red" truncate maw="40%" data-media="error">{it.error}</Text>
                  : <Text size="xs" c="dimmed">{formatted(it.size, { format: 'bytes' })}</Text>}
              </UnstyledButton>}
              {!it.error && !it.icon && <Download it={it} />}
              <Actions actions={it.icon ? it.actions?.filter((x) => !x.primary) : it.actions} max={2} quiet={i > 0} />
            </Group>
        )} />
      )}
      <Lightbox items={files} at={at} onAt={setAt} />
    </Stack>
  );
}
