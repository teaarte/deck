import { useState } from 'react';
import { Anchor, Button, Collapse, Group, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { call } from '../../lib/api';
import { notifyError } from '../../lib/notify';

// Deleted lists the cards deleted in the last day, so they come back from any device.
export function Deleted({ cards }: { cards: { id: number; title: string }[] }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  if (!cards.length) return null;
  return (
    <Stack gap={4} align="flex-start">
      <Anchor component="button" size="xs" onClick={() => setOpen(!open)}>{t('board.recently_deleted', { count: cards.length })}</Anchor>
      <Collapse expanded={open}>
        <Stack gap={4}>
          {cards.map((c) => (
            <Group key={c.id} gap="xs" data-deleted={c.id}>
              <Text size="sm" c="dimmed">{c.title}</Text>
              <Button size="compact-xs" variant="subtle" onClick={() => call('deck.card_restore', { card: c.id }).catch((e) => notifyError(e))}>{t('board.restore')}</Button>
            </Group>
          ))}
        </Stack>
      </Collapse>
    </Stack>
  );
}
