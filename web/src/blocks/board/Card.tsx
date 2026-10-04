import { useSortable } from '@dnd-kit/react/sortable';
import { Badge, Group, Paper, Text, UnstyledButton } from '@mantine/core';
import { IconGripVertical } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { BoardValue } from '../../lib/catalog.gen';

export type BoardCard = BoardValue['lanes'][number]['cards'][number];

// Card is a card on the board: it moves by its handle and opens on a click.
export function Card({ card, lane, index, onOpen }: { card: BoardCard; lane: string; index: number; onOpen: () => void }) {
  const { t } = useTranslation();
  const { ref, handleRef, isDragging } = useSortable({ id: card.id, index, group: lane, type: 'card', accept: 'card' });
  return (
    <Paper ref={ref} p="xs" radius="md" shadow="xs" data-card={card.id} style={{ opacity: isDragging ? 0.5 : 1 }}>
      <Group gap="xs" wrap="nowrap" align="flex-start">
        <Text ref={handleRef} c="dimmed" data-handle={card.id} aria-label={t('board.move')} lh={0}
          style={{ cursor: 'grab', touchAction: 'none', userSelect: 'none', padding: '2px 0' }}><IconGripVertical size={16} /></Text>
        <UnstyledButton onClick={onOpen} style={{ flex: 1, minWidth: 0 }}>
          <Text size="sm" fw={500}>{card.title}</Text>
          {card.text && <Text size="xs" c="dimmed" lineClamp={3}>{card.text}</Text>}
          <Group gap={4}>
            {(card.waits ?? []).map((w) => <Badge key={w} size="xs" variant="light">{t('board.waits', { id: w })}</Badge>)}
          </Group>
        </UnstyledButton>
      </Group>
    </Paper>
  );
}
