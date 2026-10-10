import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import { call } from '../lib/api';
import { events } from '../lib/events';
import i18n from '../lib/i18n';

export type ScreenLink = { name: string; title: string; icon?: string };

export interface MenuEntry {
  project?: string;
  title: string;
  icon?: string;
  screens: ScreenLink[];
  // open is the one screen of a project that has one, opened by its header.
  open?: ScreenLink;
  extra?: ScreenLink[];
  running?: number;
  asks?: number;
  errors?: number;
}

// Pulse is what waits for the owner, runs and fails in the workspace, and which of them the cockpit says first.
export interface Pulse { asks: number; running: number; errors: number; state: 'asks' | 'errors' | 'running' | 'calm' }

// MenuGroup is a group at the foot of the menu, its links each of a project or of deck.
export interface MenuGroup { key: string; title?: string; links: (ScreenLink & { project?: string })[] }

// Menu is what deck.menu gives: the pulse, deck's screens, the projects of the side, the master, the foot and the
// topics after which it is read again.
export interface Menu { pulse: Pulse; deck: MenuEntry; projects: MenuEntry[]; master?: MenuEntry; system: MenuGroup[]; topics: string[] }

const EMPTY: Menu = { pulse: { asks: 0, running: 0, errors: 0, state: 'calm' }, deck: { title: 'deck', screens: [] }, projects: [], system: [], topics: [] };

// entryOf is the entry of a project in the menu, deck's own without one.
export const entryOf = (m: Menu, project?: string) =>
  !project ? m.deck : m.projects.find((x) => x.project === project) ?? (m.master?.project === project ? m.master : undefined);

// MenuContext is the menu the shell read, for the parts of a screen that count what it shows.
export const MenuContext = createContext<Menu>(EMPTY);
export const useMenuData = () => useContext(MenuContext);

// useMenu is the menu of deck: read again 400 ms after a change of a topic it names and on return to the page; a
// failed read keeps the menu it had.
export function useMenu() {
  const [menu, setMenu] = useState<Menu>(EMPTY);
  const topics = useRef<string[]>([]);
  const reload = useCallback(() => {
    call<Menu>('deck.menu', { lang: i18n.language }).then((m) => { topics.current = m.topics; setMenu(m); }, () => undefined);
  }, []);
  useEffect(() => {
    reload();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const stop = events((e) => {
      if (!topics.current.includes(e.topic) || timer) return;
      timer = setTimeout(() => { timer = undefined; reload(); }, 400);
    }, reload);
    return () => { clearTimeout(timer); stop(); };
  }, []);
  return menu;
}
