import type { ReactNode } from 'react';
import { Group, Popover, Text } from '@mantine/core';
import { IconAlertTriangle } from '@tabler/icons-react';

// Failed holds why the press of a button failed under the button, until a click elsewhere or the next press.
export function Failed({ error, onClose, children }: { error: string; onClose: () => void; children: ReactNode }) {
  return (
    <Popover opened={!!error} onClose={onClose} position="bottom-start" withArrow shadow="md" radius="md" withinPortal>
      <Popover.Target>{children}</Popover.Target>
      <Popover.Dropdown p="xs" maw={360} data-failed-press>
        <Group gap={6} wrap="nowrap" align="flex-start">
          <IconAlertTriangle size={16} color="var(--mantine-color-red-filled)" style={{ flex: 'none', marginTop: 2 }} />
          <Text size="sm" c="red" style={{ overflowWrap: 'anywhere' }}>{error}</Text>
        </Group>
      </Popover.Dropdown>
    </Popover>
  );
}
