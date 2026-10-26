import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
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
  css: string;
  // images are the pictures of the theme as covers are: the scene of Today, the page behind, a tile repeated behind.
  images?: Partial<Record<'cockpit' | 'backdrop' | 'pattern', Picture>>;
}

// Picture is a picture of the theme by scheme: where it is read and its main color.
export interface Picture { light?: string; dark?: string; color?: string; dark_color?: string }

const KEY = 'deck-theme';
const BARE: Worn = { name: '', title: '', mood: '', schemes: ['light', 'dark'], font: {}, radius: 'md', density: 'default', css: '' };

// Preview is a theme and scheme a link asks for, as deck.theme_check gives them: ?theme=<name>&scheme=dark. It lasts
// while the screen of the link shows; a pick in Settings or the owner ends it.
export interface Preview { theme: string; scheme: '' | 'light' | 'dark' }

const params = new URLSearchParams(location.search);
// shot marks a page deck opens itself to take its picture: it wears a theme of a link without the bar that says so.
export const shot = params.has('shot');
const previewing = createStore<Preview>({ theme: params.get('theme') ?? '', scheme: (params.get('scheme') ?? '') as Preview['scheme'] });
export const usePreview = () => useStore(previewing);

// endPreview takes the theme and scheme of a link out of the address and wears the theme in use again.
export function endPreview() {
  if (!previewing.get().theme && !previewing.get().scheme) return;
  const url = new URL(location.href);
  url.searchParams.delete('theme');
  url.searchParams.delete('scheme');
  history.replaceState(history.state, '', url.pathname + url.search + url.hash);
  previewing.set({ theme: '', scheme: '' });
}

function cached(): Worn {
  try {
    return { ...BARE, ...(JSON.parse(localStorage.getItem(KEY) ?? 'null') ?? {}) };
  } catch {
    return BARE;
  }
}

const worn = createStore<Worn>(previewing.get().theme ? BARE : cached());

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
  const lang = useTranslation().i18n.language;
  const { theme } = usePreview();
  useEffect(() => {
    if (!on) return;
    const input = { name: theme || undefined, lang: i18n.language };
    return liveRead<Worn>('deck.theme', input, () => ['documents', 'settings', 'files'], (w) => {
      if (!w) return;
      if (!theme) localStorage.setItem(KEY, JSON.stringify(w));
      put(w);
      worn.set(w);
    });
  }, [on, lang, theme]);
}

export const useWorn = () => useStore(worn);

// forced is the scheme the page must wear: the previewed one, or the only one of the theme.
export function forced(w: Worn, p: Preview): 'light' | 'dark' | undefined {
  if (p.scheme) return p.scheme;
  return w.schemes.length === 1 ? w.schemes[0] : undefined;
}
