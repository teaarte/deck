import { createComponentImplementation } from '@a2ui/react/v0_9';
import { SimpleGrid, Stack, Title } from '@mantine/core';
import s from '../shell.module.css';
import type { Span } from './Block';
import { api, kids } from './blockImpl';

// columns puts blocks side by side, one column each on a wide screen.
export const columns = createComponentImplementation(api('columns'), ({ props, buildChild }) => {
  const p = props as { title?: string; span?: Span; children: unknown[] };
  const n = Math.max(1, (p.children ?? []).length);
  return (
    <Stack gap="xs" data-span={String(p.span ?? 'full')}>
      {p.title && <Title order={3} className={s.blockTitle}>{p.title}</Title>}
      <SimpleGrid cols={{ base: 1, sm: Math.min(2, n), lg: n }} spacing="lg" verticalSpacing="lg" data-columns>
        {kids(p.children, buildChild, s.cell)}
      </SimpleGrid>
    </Stack>
  );
});
