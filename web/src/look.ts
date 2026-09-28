import { useSyncExternalStore } from 'react';
import { createTheme, DEFAULT_THEME, type MantineThemeOverride } from '@mantine/core';

// Look is how deck looks on this device; the color scheme itself is kept by Mantine.
export interface Look {
  accent: string;
  density: 'compact' | 'default' | 'comfortable';
  font: 'small' | 'default' | 'large';
  radius: 'xs' | 'sm' | 'md' | 'lg' | 'xl';
}

const KEY = 'deck-look';
export const ACCENTS = ['blue', 'indigo', 'violet', 'grape', 'pink', 'red', 'orange', 'yellow', 'lime', 'green', 'teal', 'cyan', 'gray'];
export const DENSITIES: Look['density'][] = ['compact', 'default', 'comfortable'];
export const FONTS: Look['font'][] = ['small', 'default', 'large'];
export const RADII: Look['radius'][] = ['xs', 'sm', 'md', 'lg', 'xl'];
const DEFAULT: Look = { accent: 'blue', density: 'default', font: 'default', radius: 'md' };

function read(): Look {
  try {
    const got = JSON.parse(localStorage.getItem(KEY) ?? '{}') as Partial<Look>;
    return {
      accent: ACCENTS.includes(got.accent ?? '') ? got.accent! : DEFAULT.accent,
      density: DENSITIES.includes(got.density!) ? got.density! : DEFAULT.density,
      font: FONTS.includes(got.font!) ? got.font! : DEFAULT.font,
      radius: RADII.includes(got.radius!) ? got.radius! : DEFAULT.radius,
    };
  } catch {
    return DEFAULT;
  }
}

let look = read();
const subs = new Set<() => void>();
export function setLook(change: Partial<Look>) {
  look = { ...look, ...change };
  localStorage.setItem(KEY, JSON.stringify(look));
  subs.forEach((f) => f());
}
window.addEventListener('storage', (e) => {
  if (e.key !== KEY) return;
  look = read();
  subs.forEach((f) => f());
});
export const useLook = () => useSyncExternalStore((f) => (subs.add(f), () => subs.delete(f)), () => look);

const scaled = (sizes: Record<string, string>, by: number) =>
  Object.fromEntries(Object.entries(sizes).map(([k, v]) => [k, by === 1 ? v : `calc(${v} * ${by})`]));

// theme is the Mantine theme of a look: the accent is the primary color, density scales spacing, font the text.
export function theme(l: Look): MantineThemeOverride {
  const space = { compact: 0.75, default: 1, comfortable: 1.25 }[l.density];
  const text = { small: 0.9, default: 1, large: 1.15 }[l.font];
  const headings = Object.fromEntries(Object.entries(DEFAULT_THEME.headings.sizes).map(([k, h]) =>
    [k, { ...h, fontSize: text === 1 ? h.fontSize : `calc(${h.fontSize} * ${text})` }]));
  return createTheme({
    primaryColor: l.accent,
    defaultRadius: l.radius,
    spacing: scaled(DEFAULT_THEME.spacing, space),
    fontSizes: scaled(DEFAULT_THEME.fontSizes, text),
    headings: { sizes: headings },
  });
}
