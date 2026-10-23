import { createComponentImplementation } from '@a2ui/react/v0_9';
import { Group, Stack } from '@mantine/core';
import s from './blocks.module.css';
import { Hero } from '../cockpit/Hero';
import { IconTitle } from '../ui/IconTitle';
import type { ScreenProps } from '../lib/catalog.gen';
import { BlockContext } from '../screen/press';
import { api, children } from './blockImpl';

// screen is the root of a screen, a tab or the window of a dialog: its title with the buttons of its dialogs, and a
// grid of its blocks; the cockpit has its top in place of its title, the buttons of its dialogs in it.
export const screen = createComponentImplementation(api('screen'), ({ props, buildChild, context }) => {
  const p = props as ScreenProps;
  const dialogs = (p.dialogs?.length ?? 0) > 0;
  const cockpit = !!p.title && context.dataContext.surface.id === 'deck-cockpit';
  const heads = dialogs && children(p.dialogs).map((c) => <span key={String(c)}>{buildChild(String(c))}</span>);
  return (
    <BlockContext.Provider value={context}>
    <Stack gap="lg" data-screen data-span="full">
      {((p.title && !cockpit) || (dialogs && !cockpit)) && (
        <Group justify="space-between" align="center" gap="sm" data-screen-head>
          {p.title && !cockpit ? <IconTitle order={2} className={s.screenTitle} title={p.title} icon={p.icon} size={24} /> : <span />}
          {dialogs && <Group gap="xs">{heads}</Group>}
        </Group>
      )}
      {cockpit && <Hero dialogs={heads || undefined} />}
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
