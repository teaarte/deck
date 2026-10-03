import { useSyncExternalStore } from 'react';
import { createTheme, DEFAULT_THEME, type MantineThemeOverride } from '@mantine/core';
import { glass } from './glass';
import { palette } from './palette';

// Look is how deck looks on this device; the color scheme itself is kept by Mantine.
export interface Look {
  accent: string;
  density: 'compact' | 'default' | 'comfortable';
  font: 'small' | 'default' | 'large';
  radius: 'xs' | 'sm' | 'md' | 'lg' | 'xl';
  theme: 'default' | 'glass';
  // pattern draws the icons behind the screen; tint goes from clear (0) to opaque (1) glass.
  pattern: boolean;
  tint: number;
}

const KEY = 'deck-look';
export const ACCENTS = ['blue', 'indigo', 'violet', 'grape', 'pink', 'red', 'orange', 'yellow', 'lime', 'green', 'teal', 'cyan', 'gray'];
export const DENSITIES: Look['density'][] = ['compact', 'default', 'comfortable'];
export const FONTS: Look['font'][] = ['small', 'default', 'large'];
export const RADII: Look['radius'][] = ['xs', 'sm', 'md', 'lg', 'xl'];
export const THEMES: Look['theme'][] = ['default', 'glass'];
const DEFAULT: Look = { accent: 'blue', density: 'default', font: 'default', radius: 'md', theme: 'default', pattern: true, tint: 0.35 };

function read(): Look {
  try {
    const got = JSON.parse(localStorage.getItem(KEY) ?? '{}') as Partial<Look>;
    return {
      accent: ACCENTS.includes(got.accent ?? '') ? got.accent! : DEFAULT.accent,
      density: DENSITIES.includes(got.density!) ? got.density! : DEFAULT.density,
      font: FONTS.includes(got.font!) ? got.font! : DEFAULT.font,
      radius: RADII.includes(got.radius!) ? got.radius! : DEFAULT.radius,
      theme: THEMES.includes(got.theme!) ? got.theme! : DEFAULT.theme,
      pattern: typeof got.pattern === 'boolean' ? got.pattern : DEFAULT.pattern,
      tint: typeof got.tint === 'number' && got.tint >= 0 && got.tint <= 1 ? got.tint : DEFAULT.tint,
    };
  } catch {
    return DEFAULT;
  }
}

// The theme marks the root element: the glass styles and their backdrop hang on it.
const mark = () => {
  document.documentElement.dataset.deckTheme = look.theme;
  document.documentElement.style.setProperty('--glass-tint-user', String(look.tint));
};
let look = read();
mark();
const subs = new Set<() => void>();
export function setLook(change: Partial<Look>) {
  look = { ...look, ...change };
  localStorage.setItem(KEY, JSON.stringify(look));
  mark();
  subs.forEach((f) => f());
}
window.addEventListener('storage', (e) => {
  if (e.key !== KEY) return;
  look = read();
  mark();
  subs.forEach((f) => f());
});
export const useLook = () => useSyncExternalStore((f) => (subs.add(f), () => subs.delete(f)), () => look);

const scaled = (sizes: Record<string, string>, by: number) =>
  Object.fromEntries(Object.entries(sizes).map(([k, v]) => [k, by === 1 ? v : `calc(${v} * ${by})`]));

// theme is the Mantine theme of a look: the accent is the primary color, density scales spacing, font the text,
// and the glass theme puts glass on the shell, overlays and controls.
export function theme(l: Look): MantineThemeOverride {
  const space = { compact: 0.75, default: 1, comfortable: 1.25 }[l.density];
  const text = { small: 0.9, default: 1, large: 1.15 }[l.font];
  const headings = Object.fromEntries(Object.entries(DEFAULT_THEME.headings.sizes).map(([k, h]) =>
    [k, { ...h, fontSize: text === 1 ? h.fontSize : `calc(${h.fontSize} * ${text})` }]));
  return createTheme({
    primaryColor: l.accent,
    colors: palette,
    primaryShade: { light: 6, dark: 6 },
    autoContrast: true,
    respectReducedMotion: true,
    defaultRadius: l.radius,
    spacing: scaled(DEFAULT_THEME.spacing, space),
    fontSizes: scaled(DEFAULT_THEME.fontSizes, text),
    headings: { sizes: headings },
    components: l.theme === 'glass' ? glass : {},
  });
}
