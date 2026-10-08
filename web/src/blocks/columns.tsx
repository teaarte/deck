import { createContext, useContext, type ReactNode } from 'react';
import { createComponentImplementation } from '@a2ui/react/v0_9';
import { Stack, Title } from '@mantine/core';
import s from './blocks.module.css';
import type { Span } from './Block';
import { api, kids } from './blockImpl';

const Inside = createContext(false);

// columns puts blocks side by side, a block of span 2 wider; columns inside columns is one column of blocks one under
// another.
export const columns = createComponentImplementation(api('columns'), ({ props, buildChild }) => {
  const p = props as { title?: string; span?: Span; children: unknown[] };
  return <Columns title={p.title} span={p.span}>{kids(p.children, buildChild, s.cell)}</Columns>;
});

function Columns({ title, span, children }: { title?: string; span?: Span; children: ReactNode }) {
  const inside = useContext(Inside);
  return (
    <Stack gap="xs" data-span={String(span ?? 'full')}>
      {title && <Title order={3} className={s.blockTitle}>{title}</Title>}
      <Inside.Provider value>
        <div className={inside ? s.stack : s.cols} data-columns={inside ? undefined : true} data-column={inside || undefined}>{children}</div>
      </Inside.Provider>
    </Stack>
  );
}
