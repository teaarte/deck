import { createContext, useContext } from 'react';
import { createComponentImplementation } from '@a2ui/react/v0_9';
import { ColumnApi, RowApi } from '@a2ui/web_core/v0_9';
import s from './blocks.module.css';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import type { ReactNode } from 'react';
import { children } from './blockImpl';

// cells draws the children of a Row or Column, a child with a weight as wide as that many cells.
const cells = (ids: unknown, build: (id: string) => ReactNode, context: ComponentContext) => children(ids).map((c) => {
  const id = String(c);
  const weight = (context.surfaceComponents.get(id)?.properties as { weight?: number } | undefined)?.weight;
  return <div key={id} className={s.cell}>{weight ? <div data-span={String(weight)}>{build(id)}</div> : build(id)}</div>;
});

const Inside = createContext(false);

// row puts blocks side by side, a block of span 2 wider; it takes the full width of the screen.
export const row = createComponentImplementation(RowApi, ({ props, buildChild, context }) => (
  <div data-span="full">
    <Inside.Provider value>
      <div className={s.cols} data-columns>{cells(props.children, buildChild, context)}</div>
    </Inside.Provider>
  </div>
));

// column is blocks one under another: columns inside columns, or a container with its title over it.
export const column = createComponentImplementation(ColumnApi, ({ props, buildChild, context }) => {
  const inside = useContext(Inside);
  return <div data-span="full"><div className={s.stack} data-column={inside || undefined}>{cells(props.children, buildChild, context)}</div></div>;
});
