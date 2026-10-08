import { useEffect, useState } from 'react';
import { Group, Loader, Text } from '@mantine/core';

// Pending is a loader that shows only once the wait is long enough to notice, so a quick load does not blink.
export function Pending({ text, after = 500 }: { text?: string; after?: number }) {
  const [shown, setShown] = useState(false);
  useEffect(() => {
    const timer = setTimeout(() => setShown(true), after);
    return () => clearTimeout(timer);
  }, [after]);
  if (!shown) return null;
  return <Group gap="xs"><Loader size="xs" />{text && <Text size="sm" c="dimmed">{text}</Text>}</Group>;
}
