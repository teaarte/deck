import { useEffect, useRef, useState } from 'react';
import { DragDropProvider, useDroppable } from '@dnd-kit/react';
import { useSortable } from '@dnd-kit/react/sortable';
import { move } from '@dnd-kit/helpers';
import { Anchor, Badge, Button, Collapse, Group, Modal, Paper, ScrollArea, Stack, Text, TextInput, Textarea, UnstyledButton } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { useTranslation } from 'react-i18next';
import { call } from './api';
import { setChatCard } from './chat';
import type { BoardValue } from './catalog.gen';

type BoardCard = BoardValue['lanes'][number]['cards'][number];

type Order = Record<string, number[]>;

const orderOf = (v: BoardValue): Order => Object.fromEntries(v.lanes.map((l) => [l.name, l.cards.map((c) => c.id)]));

const failed = (e: unknown) => notifications.show({ message: (e as Error).message, color: 'red', autoClose: 10000 });

function Card({ card, lane, index, onOpen }: { card: BoardCard; lane: string; index: number; onOpen: () => void }) {
  const { t } = useTranslation();
  const { ref, handleRef, isDragging } = useSortable({ id: card.id, index, group: lane, type: 'card', accept: 'card' });
  return (
    <Paper ref={ref} withBorder p="xs" data-card={card.id} style={{ opacity: isDragging ? 0.5 : 1 }}>
      <Group gap="xs" wrap="nowrap" align="flex-start">
        <Text ref={handleRef} c="dimmed" data-handle={card.id} aria-label={t('board.move')}
          style={{ cursor: 'grab', touchAction: 'none', userSelect: 'none', padding: '0 4px' }}>⠿</Text>
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

function Lane({ name, title, children, adding }: { name: string; title: string; children: React.ReactNode; adding?: React.ReactNode }) {
  const { ref } = useDroppable({ id: name, type: 'lane', accept: 'card', collisionPriority: 0 });
  return (
    <Paper ref={ref} bg="gray.0" withBorder p="xs" w={260} miw={260} data-lane={name} style={{ minHeight: 160 }}>
      <Stack gap="xs">
        <Text fw={600} size="sm">{title}</Text>
        {children}
        {adding}
      </Stack>
    </Paper>
  );
}

function NewCard({ project, lane }: { project: string; lane: string }) {
  const { t } = useTranslation();
  const [title, setTitle] = useState('');
  const [busy, setBusy] = useState(false);
  const add = async () => {
    if (!title.trim() || busy) return;
    setBusy(true);
    try {
      await call('deck.card_create', { project, lane, title: title.trim() });
      setTitle('');
    } catch (e) {
      failed(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <TextInput size="xs" placeholder={t('board.new')} value={title} disabled={busy} data-new-card
      onChange={(e) => setTitle(e.currentTarget.value)} onKeyDown={(e) => { if (e.key === 'Enter') void add(); }} />
  );
}

// Deleted lists the cards deleted in the last day, so they come back from any device.
function Deleted({ cards }: { cards: { id: number; title: string }[] }) {
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
              <Button size="compact-xs" variant="subtle" onClick={() => call('deck.card_restore', { card: c.id }).catch(failed)}>{t('board.restore')}</Button>
            </Group>
          ))}
        </Stack>
      </Collapse>
    </Stack>
  );
}

function CardEditor({ card, onClose }: { card: BoardCard; onClose: () => void }) {
  const { t } = useTranslation();
  useEffect(() => { setChatCard(card.id); return () => setChatCard(undefined); }, [card.id]);
  const [title, setTitle] = useState(card.title);
  const [text, setText] = useState(card.text ?? '');
  const [busy, setBusy] = useState(false);
  const run = async (f: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await f();
      onClose();
    } catch (e) {
      failed(e);
    } finally {
      setBusy(false);
    }
  };
  const remove = () => run(async () => {
    await call('deck.card_delete', { card: card.id });
    const id = notifications.show({
      autoClose: 10000,
      message: (
        <Group justify="space-between" wrap="nowrap">
          <Text size="sm">{t('board.deleted', { title: card.title })}</Text>
          <Button size="xs" variant="subtle" data-undo onClick={() => {
            notifications.hide(id);
            call('deck.card_restore', { card: card.id }).catch(failed);
          }}>{t('verb.undo')}</Button>
        </Group>
      ),
    });
  });
  return (
    <Modal opened onClose={onClose} title={t('board.card', { id: card.id })}>
      <Stack gap="sm">
        <TextInput label={t('board.title')} value={title} onChange={(e) => setTitle(e.currentTarget.value)} />
        <Textarea label={t('board.text')} autosize minRows={3} value={text} onChange={(e) => setText(e.currentTarget.value)} />
        <Group justify="space-between">
          <Button color="red" variant="subtle" loading={busy} onClick={remove}>{t('board.delete')}</Button>
          <Button loading={busy} disabled={!title.trim()} onClick={() => run(() => call('deck.card_update', { card: card.id, title, text }))}>
            {t('board.save')}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}

// Board draws the board of a project; a card moves by its handle, which on a phone takes a long press.
export function Board({ value }: { value: BoardValue }) {
  const [order, setOrder] = useState<Order>(() => orderOf(value));
  const [open, setOpen] = useState<BoardCard>();
  const dragging = useRef(false);
  const before = useRef<Order>(order);
  const current = useRef<Order>(order);
  const put = (o: Order) => { current.current = o; setOrder(o); };
  useEffect(() => {
    if (!dragging.current) put(orderOf(value));
  }, [value]);
  const cards = new Map(value.lanes.flatMap((l) => l.cards.map((c) => [c.id, c] as const)));
  const inbox = value.lanes.find((l) => l.role === 'inbox') ?? value.lanes[0];
  return (
    <>
      <DragDropProvider
        onDragStart={() => { dragging.current = true; before.current = current.current; }}
        onDragOver={(e) => put(move(current.current, e))}
        onDragEnd={(e) => {
          dragging.current = false;
          if (e.canceled) {
            put(before.current);
            return;
          }
          const order = current.current;
          const id = Number(e.operation.source?.id);
          const lane = Object.keys(order).find((k) => order[k].includes(id));
          if (!lane) return;
          const list = order[lane];
          const next = list[list.indexOf(id) + 1];
          const was = Object.keys(before.current).find((k) => before.current[k].includes(id));
          if (was === lane && before.current[lane].join() === list.join()) return;
          call('deck.card_move', next ? { card: id, lane, before: next } : { card: id, lane }).catch((err) => {
            put(orderOf(value));
            failed(err);
          });
        }}>
        <ScrollArea type="auto" offsetScrollbars>
          <Group align="flex-start" wrap="nowrap" gap="sm">
            {value.lanes.map((l) => (
              <Lane key={l.name} name={l.name} title={l.title}
                adding={l.name === inbox?.name ? <NewCard project={value.project} lane={l.name} /> : undefined}>
                {(order[l.name] ?? []).map((id, i) => {
                  const c = cards.get(id);
                  return c ? <Card key={id} card={c} lane={l.name} index={i} onOpen={() => setOpen(c)} /> : null;
                })}
              </Lane>
            ))}
          </Group>
        </ScrollArea>
      </DragDropProvider>
      <Deleted cards={value.deleted ?? []} />
      {open && <CardEditor card={open} onClose={() => setOpen(undefined)} />}
    </>
  );
}
