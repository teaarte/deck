import { useEffect, useRef, useState } from 'react';
import { move } from '@dnd-kit/helpers';
import type { DragEndEvent, DragOverEvent } from '@dnd-kit/react';
import { call } from '../../lib/api';
import { notifyError } from '../../lib/notify';
import type { BoardValue } from '../../lib/catalog.gen';

type Order = Record<string, number[]>;

const orderOf = (v: BoardValue): Order => Object.fromEntries(v.lanes.map((l) => [l.name, l.cards.map((c) => c.id)]));

// useBoardOrder is the order of the cards in their lanes: it follows the server except during a drag, and a drop
// moves the card on the server, back where it was should the move fail.
export function useBoardOrder(value: BoardValue) {
  const [order, setOrder] = useState<Order>(() => orderOf(value));
  const dragging = useRef(false);
  const before = useRef<Order>(order);
  const current = useRef<Order>(order);
  const put = (o: Order) => { current.current = o; setOrder(o); };
  useEffect(() => {
    if (!dragging.current) put(orderOf(value));
  }, [value]);
  const onDragStart = () => { dragging.current = true; before.current = current.current; };
  const onDragOver = (e: DragOverEvent) => put(move(current.current, e));
  const onDragEnd = (e: DragEndEvent) => {
    dragging.current = false;
    if (e.canceled) {
      put(before.current);
      return;
    }
    const now = current.current;
    const id = Number(e.operation.source?.id);
    const lane = Object.keys(now).find((k) => now[k].includes(id));
    if (!lane) return;
    const list = now[lane];
    const next = list[list.indexOf(id) + 1];
    const was = Object.keys(before.current).find((k) => before.current[k].includes(id));
    if (was === lane && before.current[lane].join() === list.join()) return;
    call('deck.card_move', next ? { card: id, lane, before: next } : { card: id, lane }).catch((err) => {
      put(orderOf(value));
      notifyError(err);
    });
  };
  return { order, onDragStart, onDragOver, onDragEnd };
}
