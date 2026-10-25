import { createContext, useContext } from 'react';
import i18n from '../lib/i18n';
import { useLiveRead } from './useLiveRead';

export type ScreenLink = { name: string; title: string; icon?: string };

export interface MenuEntry {
  project?: string;
  title: string;
  icon?: string;
  screens: ScreenLink[];
  // open is the one screen of a project that has one.
  open?: ScreenLink;
  extra?: ScreenLink[];
  running?: number;
  asks?: number;
  errors?: number;
  // cover is where the light copy of the picture of the project's card in the light scheme is read, cover_dark in
  // the dark one; their colors are shown while they load.
  cover?: string;
  cover_dark?: string;
  cover_color?: string;
  cover_dark_color?: string;
  // doing is the title of the command the project runs now, next the next run of its schedule.
  doing?: string;
  next?: { title: string; at: string };
}

// Pulse is what waits for the owner, runs and fails in the workspace, which of them Today says first, and the first
// line of Today deck writes with the mark beside it.
export interface Pulse {
  asks: number; running: number; errors: number; failed?: number; notes?: number; state: 'asks' | 'errors' | 'talking' | 'running' | 'calm';
  line?: string; mark?: string;
}

// MenuGroup is a group at the foot of the menu, its links each of a project or of deck.
export interface MenuGroup { key: string; title?: string; links: (ScreenLink & { project?: string })[] }

// Menu is what deck.menu gives: the pulse, deck's screens, the projects of the side, the master, the foot and the
// topics after which it is read again.
export interface Menu { pulse: Pulse; deck: MenuEntry; projects: MenuEntry[]; master?: MenuEntry; system: MenuGroup[]; language?: string; topics: string[] }

const EMPTY: Menu = { pulse: { asks: 0, running: 0, errors: 0, state: 'calm' }, deck: { title: 'deck', screens: [] }, projects: [], system: [], topics: [] };

// entryOf is the entry of a project in the menu, deck's own without one.
export const entryOf = (m: Menu, project?: string) =>
  !project ? m.deck : m.projects.find((x) => x.project === project) ?? (m.master?.project === project ? m.master : undefined);

// MenuContext is the menu the shell read, for the parts of a screen that count what it shows.
export const MenuContext = createContext<Menu>(EMPTY);
export const useMenuData = () => useContext(MenuContext);

// useMenu is the menu of deck, read again after a change of a topic it names.
export const useMenu = () => useLiveRead<Menu>('deck.menu', { lang: i18n.language }, (m) => m?.topics ?? [], EMPTY);

// Place is a screen to open, of a project or of deck.
export type Place = ScreenLink & { project?: string };

// placesOf are the screens of a project, its files and settings last; deck's are the cockpit and deck's own screens.
export function placesOf(m: Menu, project?: string): Place[] {
  if (!project) {
    const own = m.system.flatMap((g) => g.links).filter((l) => !l.project);
    return [{ name: 'cockpit', title: i18n.t('nav.cockpit'), icon: 'sun' }, ...own];
  }
  const e = entryOf(m, project);
  return e ? [...e.screens, ...(e.extra ?? [])].map((l) => ({ ...l, project })) : [];
}
