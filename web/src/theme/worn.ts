import { useEffect } from 'react';
import i18n from '../lib/i18n';
import { liveRead } from '../lib/live';
import { createStore, useStore } from '../lib/store';

// Worn is the theme the whole deck wears, as deck.theme gives it: the CSS of its tokens, parts and rules is checked
// and written by the server, the client only puts it on the page.
export interface Worn {
  name: string;
  title: string;
  mood: string;
  schemes: ('light' | 'dark')[];
  accent?: string;
  font: { text?: string; headings?: string };
  radius: '0' | 'xs' | 'sm' | 'md' | 'lg' | 'xl';
  density: 'compact' | 'default' | 'comfortable';
  backdrop: 'pattern' | 'sky' | 'plain';
  css: string;
}

const KEY = 'deck-theme';
const BARE: Worn = { name: '', title: '', mood: '', schemes: ['light', 'dark'], font: {}, radius: 'md', density: 'default', backdrop: 'plain', css: '' };

// preview is a theme and scheme the address asks for, as deck.theme_check gives them: ?theme=<name>&scheme=dark.
const params = new URLSearchParams(location.search);
export const preview = { theme: params.get('theme') ?? '', scheme: (params.get('scheme') ?? '') as '' | 'light' | 'dark' };

function cached(): Worn {
  try {
    return { ...BARE, ...(JSON.parse(localStorage.getItem(KEY) ?? 'null') ?? {}) };
  } catch {
    return BARE;
  }
}

const worn = createStore<Worn>(preview.theme ? BARE : cached());

// put writes the theme on the page: its CSS in one style element and its name on the root.
function put(w: Worn) {
  let el = document.getElementById(KEY) as HTMLStyleElement | null;
  if (!el) {
    el = document.createElement('style');
    el.id = KEY;
    document.head.appendChild(el);
  }
  if (el.textContent !== w.css) el.textContent = w.css;
  document.documentElement.dataset.deckTheme = w.name;
}
put(worn.get());

// useWear follows the theme in use, or the previewed one, and keeps the last one for the next load.
export function useWear(on: boolean) {
  useEffect(() => {
    if (!on) return;
    const input = { name: preview.theme || undefined, lang: i18n.language };
    return liveRead<Worn>('deck.theme', input, () => ['documents', 'settings'], (w) => {
      if (!w) return;
      if (!preview.theme) localStorage.setItem(KEY, JSON.stringify(w));
      put(w);
      worn.set(w);
    });
  }, [on]);
}

export const useWorn = () => useStore(worn);

// forced is the scheme the page must wear: the previewed one, or the only one of the theme.
export function forced(w: Worn): 'light' | 'dark' | undefined {
  if (preview.scheme) return preview.scheme;
  return w.schemes.length === 1 ? w.schemes[0] : undefined;
}
