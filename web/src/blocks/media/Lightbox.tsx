import { useEffect } from 'react';
import { ActionIcon, Group, Modal, Text } from '@mantine/core';
import { IconChevronLeft, IconChevronRight } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import { Actions } from '../../ui/actions/Actions';
import { Download } from './Download';
import type { Item } from './kinds';
import { Viewer } from './Viewer';

// Lightbox opens one file of a set over the screen, with the one before and after it by button or arrow key.
export function Lightbox({ items, at, onAt }: { items: Item[]; at: number | null; onAt: (i: number | null) => void }) {
  const { t } = useTranslation();
  const it = at === null ? undefined : items[at];
  const step = (d: number) => at !== null && onAt((at + d + items.length) % items.length);
  useEffect(() => {
    if (at === null) return;
    const key = (e: KeyboardEvent) => {
      if (e.key === 'ArrowLeft') step(-1);
      if (e.key === 'ArrowRight') step(1);
    };
    window.addEventListener('keydown', key);
    return () => window.removeEventListener('keydown', key);
  }, [at, items.length]);
  return (
    <Modal opened={!!it} onClose={() => onAt(null)} size="xl" centered data-lightbox
      title={it && <Group gap="xs" wrap="nowrap"><Text size="sm" fw={600} truncate>{it.title || it.name}</Text>
        {items.length > 1 && <Text size="xs" c="dimmed">{(at ?? 0) + 1} / {items.length}</Text>}</Group>}>
      {it && (
        <>
          <Viewer key={String(it.id) + at} it={it} />
          <Group justify="space-between" mt="sm">
            <Group gap={4}>
              <ActionIcon variant="default" onClick={() => step(-1)} disabled={items.length < 2} aria-label={t('files.previous')} data-lightbox-prev><IconChevronLeft size={16} /></ActionIcon>
              <ActionIcon variant="default" onClick={() => step(1)} disabled={items.length < 2} aria-label={t('files.next')} data-lightbox-next><IconChevronRight size={16} /></ActionIcon>
            </Group>
            <Group gap={4}>{!it.error && <Download it={it} />}<Actions actions={it.actions} max={2} /></Group>
          </Group>
        </>
      )}
    </Modal>
  );
}
