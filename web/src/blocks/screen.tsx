import { createComponentImplementation } from '@a2ui/react/v0_9';
import { Group, Stack } from '@mantine/core';
import s from './blocks.module.css';
import { IconTitle } from '../ui/IconTitle';
import { api, kids } from './blockImpl';

// screen is the root of a screen: its title with the buttons of its dialogs, and a grid of its blocks.
export const screen = createComponentImplementation(api('screen'), ({ props, buildChild }) => {
  const p = props as { title?: string; icon?: string; children: unknown; dialogs?: unknown };
  const dialogs = Array.isArray(p.dialogs) && p.dialogs.length > 0;
  return (
    <Stack gap="lg" data-screen data-span="full">
      {(p.title || dialogs) && (
        <Group justify="space-between" align="center" gap="sm" data-screen-head>
          {p.title ? <IconTitle order={2} className={s.screenTitle} title={p.title} icon={p.icon} size={24} /> : <span />}
          {dialogs && <Group gap="xs">{kids(p.dialogs, buildChild)}</Group>}
        </Group>
      )}
      <div className={s.grid} data-grid>{kids(p.children, buildChild, s.cell)}</div>
    </Stack>
  );
});
