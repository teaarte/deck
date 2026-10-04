import type { ReactNode } from 'react';
import { useDroppable } from '@dnd-kit/react';
import { Paper, Stack, Text } from '@mantine/core';

// Lane is a column of the board that takes cards dropped on it.
export function Lane({ name, title, children, adding }: { name: string; title: string; children: ReactNode; adding?: ReactNode }) {
  const { ref } = useDroppable({ id: name, type: 'lane', accept: 'card', collisionPriority: 0 });
  return (
    <Paper ref={ref} bg="var(--mantine-color-default-hover)" p="sm" radius="lg" w={264} miw={264} data-lane={name} style={{ minHeight: 160 }}>
      <Stack gap="xs">
        <Text fw={600} size="sm">{title}</Text>
        {children}
        {adding}
      </Stack>
    </Paper>
  );
}
