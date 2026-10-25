import { useEffect, useState } from 'react';
import { Skeleton, Stack } from '@mantine/core';

// Pending is the shape of what loads, in lines of the theme's skeleton, shown only once the wait is long enough to
// notice, so a quick load does not blink; its text is for screen readers.
export function Pending({ text, lines = 3, after = 300 }: { text?: string; lines?: number; after?: number }) {
  const [shown, setShown] = useState(false);
  useEffect(() => {
    const timer = setTimeout(() => setShown(true), after);
    return () => clearTimeout(timer);
  }, [after]);
  // What waits less than a glance shows nothing yet, but says it waits, so deck's own shot waits for it too.
  if (!shown) return <span hidden data-awaits />;
  return (
    <Stack gap={10} aria-busy aria-label={text} data-pending>
      {Array.from({ length: lines }, (_, i) => <Skeleton key={i} height={12} width={i === lines - 1 ? '60%' : '100%'} />)}
    </Stack>
  );
}
