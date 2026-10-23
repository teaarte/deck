import { createContext, useContext } from 'react';
import { createComponentImplementation } from '@a2ui/react/v0_9';
import { ColumnApi, RowApi } from '@a2ui/web_core/v0_9';
import s from './blocks.module.css';
import { kids } from './blockImpl';

const Inside = createContext(false);

// row puts blocks side by side, a block of span 2 wider; it takes the full width of the screen.
export const row = createComponentImplementation(RowApi, ({ props, buildChild }) => (
  <div data-span="full">
    <Inside.Provider value>
      <div className={s.cols} data-columns>{kids(props.children, buildChild, s.cell)}</div>
    </Inside.Provider>
  </div>
));

// column is blocks one under another: columns inside columns, or a container with its title over it.
export const column = createComponentImplementation(ColumnApi, ({ props, buildChild }) => {
  const inside = useContext(Inside);
  return <div data-span="full"><div className={s.stack} data-column={inside || undefined}>{kids(props.children, buildChild, s.cell)}</div></div>;
});
