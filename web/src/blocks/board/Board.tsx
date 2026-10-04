import { useEffect, useState } from 'react';
import { DragDropProvider } from '@dnd-kit/react';
import { Group, ScrollArea } from '@mantine/core';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import type { BoardValue } from '../../lib/catalog.gen';
import { Card, type BoardCard } from './Card';
import { CardEditor } from './CardEditor';
import { Deleted } from './Deleted';
import { Lane } from './Lane';
import { NewCard } from './NewCard';
import { useBoardOrder } from './useBoardOrder';

// Board draws the board of a project; a card moves by its handle, which on a phone takes a long press.
export function Board({ value, context }: { value: BoardValue; context: ComponentContext }) {
  const { order, onDragStart, onDragOver, onDragEnd } = useBoardOrder(value);
  const [open, setOpen] = useState<BoardCard>();
  const cards = new Map(value.lanes.flatMap((l) => l.cards.map((c) => [c.id, c] as const)));
  useEffect(() => {
    if (open && !cards.has(open.id)) setOpen(undefined);
  }, [value]);
  const inbox = value.lanes.find((l) => l.role === 'inbox') ?? value.lanes[0];
  return (
    <>
      <DragDropProvider onDragStart={onDragStart} onDragOver={onDragOver} onDragEnd={onDragEnd}>
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
      {open && <CardEditor card={open} context={context} onClose={() => setOpen(undefined)} />}
    </>
  );
}
