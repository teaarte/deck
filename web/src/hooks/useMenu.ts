import { createContext, useCallback, useContext, useEffect, useState } from 'react';
import { call } from '../lib/api';
import { events } from '../lib/events';
import i18n from '../lib/i18n';

export interface MenuEntry {
  project?: string;
  title: string;
  icon?: string;
  screens: { name: string; title: string; icon?: string }[];
  extra?: { name: string; title: string; icon?: string }[];
  running?: number;
  asks?: number;
  errors?: number;
}

// MenuContext is the menu the shell read, for the parts of a screen that count what it shows.
export const MenuContext = createContext<MenuEntry[]>([]);
export const useMenuEntries = () => useContext(MenuContext);

const PULSE = ['processes', 'asks', 'health', 'projects'];

// useMenu is the menu of deck: read again 400 ms after a change of what it shows and on return to the page; a failed
// read keeps the menu it had.
export function useMenu() {
  const [menu, setMenu] = useState<MenuEntry[]>([]);
  const reload = useCallback(() => { call<MenuEntry[]>('deck.menu', { lang: i18n.language }).then(setMenu, () => undefined); }, []);
  useEffect(() => {
    reload();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const stop = events((e) => {
      if (!PULSE.includes(e.topic) || timer) return;
      timer = setTimeout(() => { timer = undefined; reload(); }, 400);
    }, reload);
    return () => { clearTimeout(timer); stop(); };
  }, []);
  return { menu, reload };
}
