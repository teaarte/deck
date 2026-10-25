import { useContext, useEffect } from 'react';
import { createComponentImplementation } from '@a2ui/react/v0_9';
import { Group, Stack } from '@mantine/core';
import s from './blocks.module.css';
import { IconTitle } from '../ui/IconTitle';
import type { ScreenProps } from '../lib/catalog.gen';
import { ScreenLead } from '../screen/lead';
import { BlockContext, readBlock } from '../screen/press';
import { api, children } from './blockImpl';

// screen is the root of a screen, a tab or the window of a dialog: its title with what the frame puts beside it and
// its dialogs' buttons, the frame's lead under it, and a grid of its blocks.
export const screen = createComponentImplementation(api('screen'), ({ props, buildChild, context }) => {
  const p = props as ScreenProps;
  const dialogs = (p.dialogs?.length ?? 0) > 0;
  const root = !p.name && !!p.title;
  const lead = useContext(ScreenLead);
  const heads = dialogs && children(p.dialogs).map((c) => <span key={String(c)}>{buildChild(String(c))}</span>);
  // The window of a dialog asks for its blocks once it is open.
  useEffect(() => { if (p.name) readBlock(context, 'dialog', { dialog: p.name }); }, [p.name, context]);
  return (
    <BlockContext.Provider value={context}>
    <Stack gap="lg" data-screen data-span="full">
      {(p.title || dialogs) && (
        <Group justify="space-between" align="center" gap="sm" data-screen-head>
          {p.title ? <IconTitle order={2} className={s.screenTitle} title={p.title} icon={p.icon} size={24} /> : <span />}
          {(dialogs || (root && lead.beside)) && <Group gap="sm">{root && lead.beside}{heads}</Group>}
        </Group>
      )}
      {root && lead.under}
      <div className={s.grid} data-grid>
        {children(p.children).map((c) => {
          const id = String(c);
          return <div key={id} className={s.cell} data-span={p.spans?.[id]}>{buildChild(id)}</div>;
        })}
      </div>
    </Stack>
    </BlockContext.Provider>
  );
});
