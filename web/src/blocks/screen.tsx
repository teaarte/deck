import { createComponentImplementation } from '@a2ui/react/v0_9';
import { Group, Stack } from '@mantine/core';
import s from './blocks.module.css';
import { Hero } from '../screen/Hero';
import { useScreenRoute } from '../screen/ScreenActions';
import { IconTitle } from '../ui/IconTitle';
import { api, kids } from './blockImpl';

// screen is the root of a screen: its title with the buttons of its dialogs, and a grid of its blocks; the cockpit has
// its top in place of its title, the buttons of its dialogs in it.
export const screen = createComponentImplementation(api('screen'), ({ props, buildChild }) => {
  const p = props as { title?: string; icon?: string; children: unknown; dialogs?: unknown };
  const dialogs = Array.isArray(p.dialogs) && p.dialogs.length > 0;
  const route = useScreenRoute();
  const cockpit = !!p.title && !!route && !route.project && route.screen === 'cockpit';
  return (
    <Stack gap="lg" data-screen data-span="full">
      {((p.title && !cockpit) || (dialogs && !cockpit)) && (
        <Group justify="space-between" align="center" gap="sm" data-screen-head>
          {p.title && !cockpit ? <IconTitle order={2} className={s.screenTitle} title={p.title} icon={p.icon} size={24} /> : <span />}
          {dialogs && <Group gap="xs">{kids(p.dialogs, buildChild)}</Group>}
        </Group>
      )}
      {cockpit && <Hero dialogs={dialogs && kids(p.dialogs, buildChild)} />}
      <div className={s.grid} data-grid>{kids(p.children, buildChild, s.cell)}</div>
    </Stack>
  );
});
