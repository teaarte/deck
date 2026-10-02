import type { MantineThemeComponents } from '@mantine/core';
import g from './glass.module.css';

const dropdown = { classNames: { dropdown: g.popover } };
const sheet = { classNames: { content: g.sheet }, defaultProps: { overlayProps: { backgroundOpacity: 0.25 } } };

// glass is the look of the shell, overlays and controls in the glass theme; the content of blocks stays solid.
export const glass: MantineThemeComponents = {
  AppShell: { classNames: { header: g.bar, navbar: g.panel, main: g.main } },
  Modal: sheet,
  Drawer: sheet,
  Popover: dropdown,
  Menu: dropdown,
  HoverCard: dropdown,
  Combobox: dropdown,
  Select: dropdown,
  MultiSelect: dropdown,
  TagsInput: dropdown,
  Autocomplete: dropdown,
  Notification: { classNames: { root: g.popover } },
  Tabs: { classNames: { list: g.tabs, tab: g.tab } },
};
