import { useRef, useState, type KeyboardEvent, type PointerEvent } from 'react';

// useResizable resizes a panel by a drag of its left edge: the width is local while the drag goes and is kept on
// release; arrow keys step it. It stays between min and maxRatio of the window.
export function useResizable({ min, maxRatio, value, onCommit, onDragging }: {
  min: number; maxRatio: number; value: number; onCommit: (w: number) => void; onDragging?: (on: boolean) => void;
}) {
  const [live, setLive] = useState<number>();
  const from = useRef({ x: 0, w: 0 });
  const width = live ?? value;
  const clamp = (w: number) => Math.round(Math.min(Math.max(min, window.innerWidth * maxRatio), Math.max(min, w)));
  const down = (e: PointerEvent) => {
    from.current = { x: e.clientX, w: width };
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    setLive(width);
    onDragging?.(true);
  };
  const move = (e: PointerEvent) => {
    if (live === undefined) return;
    setLive(clamp(from.current.w + from.current.x - e.clientX));
  };
  const up = () => {
    if (live === undefined) return;
    onCommit(live);
    setLive(undefined);
    onDragging?.(false);
  };
  const key = (e: KeyboardEvent) => {
    const step = e.key === 'ArrowLeft' ? 24 : e.key === 'ArrowRight' ? -24 : 0;
    if (!step) return;
    e.preventDefault();
    onCommit(clamp(width + step));
  };
  return {
    width,
    dragging: live !== undefined,
    props: { onPointerDown: down, onPointerMove: move, onPointerUp: up, onPointerCancel: up, onKeyDown: key, tabIndex: 0, 'aria-valuenow': width, 'aria-valuemin': min },
  };
}
