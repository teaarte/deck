import { Text } from '@mantine/core';
import type { MenuEntry } from '../hooks/useMenu';
import { Cover } from '../ui/Cover';
import { NamedIcon } from '../ui/NamedIcon';
import { href } from './router';
import s from './shell.module.css';

// ModuleMark is the mark of a module: its cover, else its icon.
export function ModuleMark({ entry }: { entry: MenuEntry }) {
  return entry.cover
    ? <Cover cover={{ light: entry.cover, dark: entry.cover_dark, color: entry.cover_color, dark_color: entry.cover_dark_color }} className={s.moduleCover} />
    : <span className={s.moduleIcon}><NamedIcon name={entry.icon} fallback={entry.title} size={16} /></span>;
}

// ModuleHead is the mark and the name of the module the owner is in, a link to its first screen, beside its tabs.
export function ModuleHead({ entry }: { entry: MenuEntry }) {
  const first = entry.screens[0]?.name ?? entry.open?.name;
  return (
    <a className={s.moduleHead} href={first ? href({ project: entry.project, screen: first }) : undefined} data-module-head>
      <ModuleMark entry={entry} />
      <Text fw={700} size="sm" truncate>{entry.title}</Text>
    </a>
  );
}
