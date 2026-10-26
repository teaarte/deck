import { createContext, useContext, useEffect } from 'react';
import { createComponentImplementation } from '@a2ui/react/v0_9';
import { Group, Stack } from '@mantine/core';
import s from './blocks.module.css';
import { IconTitle } from '../ui/IconTitle';
import type { ScreenProps } from '../lib/catalog.gen';
import { ScreenLead } from '../screen/lead';
import { BlockContext, readBlock } from '../screen/press';
import { Fit } from './around';
import { api, children } from './blockImpl';
import { Side } from './side';

// screen is the root of a screen, a tab or the window of a dialog: its title with what the frame puts beside it and
// its dialogs' buttons, the frame's lead under it, and a grid of its blocks.
export const screen = createComponentImplementation(api('screen'), ({ props, buildChild, context }) => {
  const p = props as ScreenProps;
  const dialogs = (p.dialogs?.length ?? 0) > 0;
  const root = !p.name && !!p.title;
  const lead = useContext(ScreenLead);
  const inTab = useContext(InTab);
  const fit = useContext(Fit);
  const heads = dialogs && children(p.dialogs).map((c) => <span key={String(c)}>{buildChild(String(c))}</span>);
  // A title the tab of the frame already says is not said again; the lead takes its place.
  const titled = !!p.title && !(root && lead.place === p.title);
  const under = root && lead.under;
  // The window of a dialog asks for its blocks once it is open, a tab once it is shown.
  useEffect(() => {
    if (p.name) readBlock(context, 'dialog', { dialog: p.name });
    else if (p.key) readBlock(context, 'tab', { param: inTab ? 'sub' : 'tab', key: p.key, children: children(p.children).map(String) });
  }, [p.name, p.key, context]);
  const side = children(p.side);
  const body = (
    <Stack gap="lg" data-screen data-span="full">
      {(titled || dialogs || under || (root && lead.beside)) && (
        <Group justify="space-between" align="center" gap="sm" data-screen-head>
          {titled ? <IconTitle order={2} className={s.screenTitle} title={String(p.title)} icon={p.icon} size={24} /> : under || <span />}
          {(dialogs || (root && lead.beside)) && <Group gap="sm">{root && lead.beside}{heads}</Group>}
        </Group>
      )}
      {titled && under}
      <div className={s.grid} data-grid data-fit={fit || undefined}>
        {children(p.children).map((c) => {
          const id = String(c);
          return <div key={id} className={s.cell} data-span={p.spans?.[id]}>{buildChild(id)}</div>;
        })}
      </div>
    </Stack>
  );
  return (
    <InTab.Provider value={inTab || !!p.key}>
    <BlockContext.Provider value={context}>
      {side.length > 0 ? <Side slot={lead.slot} blocks={side.map((c) => <div key={String(c)}>{buildChild(String(c))}</div>)}>{body}</Side> : body}
    </BlockContext.Provider>
    </InTab.Provider>
  );
});

// InTab is set inside a tab: a tab there is read by the key of the tab inside one.
const InTab = createContext(false);
