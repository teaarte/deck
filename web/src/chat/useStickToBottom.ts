import { useEffect, useRef } from 'react';

// useStickToBottom keeps the feed on its last reply while the owner is at the bottom, also when a reply grows after
// it is drawn; a scroll up by the owner lets it go.
export function useStickToBottom(last: unknown, lines: unknown, topic: unknown) {
  const viewport = useRef<HTMLDivElement>(null);
  const content = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const top = useRef(0);
  // touched is when the owner last moved the feed; a scroll the browser makes when the feed shrinks is not theirs.
  const touched = useRef(0);
  const down = () => { if (viewport.current && stick.current) viewport.current.scrollTop = viewport.current.scrollHeight; };
  useEffect(() => {
    const ro = new ResizeObserver(down);
    if (content.current) ro.observe(content.current);
    if (viewport.current) ro.observe(viewport.current);
    return () => ro.disconnect();
  }, []);
  useEffect(down, [last, lines]);
  useEffect(() => { stick.current = true; down(); }, [topic]);
  return {
    viewport,
    content,
    hand: () => { touched.current = Date.now(); },
    onScroll: ({ y }: { y: number }) => {
      const v = viewport.current;
      if (!v) return;
      if (y < top.current && Date.now() - touched.current < 800) stick.current = false;
      if (v.scrollHeight - y - v.clientHeight < 48) stick.current = true;
      top.current = y;
    },
    follow: () => { stick.current = true; down(); },
    keep: () => { stick.current = true; },
  };
}
