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
  theme: 'juce' | 'default' | 'glass' | 'pixel' | 'neon' | 'paper';
  // surface is how much of the accent the islands and blocks take.
  surface: 'neutral' | 'tinted' | 'vivid';
  // pattern draws the icons behind the screen; tint goes from clear (0) to opaque (1) glass.
  pattern: boolean;
  tint: number;
  // mascot shows Deki in the chat and on the cockpit; motion keeps the animations; sky draws the clouds of juce;
  // slim folds the top block of the cockpit.
  mascot: boolean;
  motion: boolean;
  sky: boolean;
  slim: boolean;
}

const KEY = 'deck-look';
export const ACCENTS = ['coral', 'blue', 'indigo', 'violet', 'grape', 'pink', 'red', 'orange', 'yellow', 'lime', 'green', 'teal', 'cyan', 'gray'];
export const DENSITIES: Look['density'][] = ['compact', 'default', 'comfortable'];
export const FONTS: Look['font'][] = ['small', 'default', 'large'];
export const RADII: Look['radius'][] = ['xs', 'sm', 'md', 'lg', 'xl'];
export const THEMES: Look['theme'][] = ['juce', 'default', 'glass', 'pixel', 'neon', 'paper'];
export const SURFACES: Look['surface'][] = ['neutral', 'tinted', 'vivid'];
const DEFAULT: Look = { accent: 'coral', density: 'default', font: 'default', radius: 'md', theme: 'juce', surface: 'neutral', pattern: true, tint: 0.35,
  mascot: true, motion: true, sky: true, slim: false };

function read(): Look {
  try {
    const got = JSON.parse(localStorage.getItem(KEY) ?? '{}') as Partial<Look>;
    return {
      accent: ACCENTS.includes(got.accent ?? '') ? got.accent! : DEFAULT.accent,
      density: DENSITIES.includes(got.density!) ? got.density! : DEFAULT.density,
      font: FONTS.includes(got.font!) ? got.font! : DEFAULT.font,
      radius: RADII.includes(got.radius!) ? got.radius! : DEFAULT.radius,
      theme: THEMES.includes(got.theme!) ? got.theme! : DEFAULT.theme,
      surface: SURFACES.includes(got.surface!) ? got.surface! : DEFAULT.surface,
      pattern: typeof got.pattern === 'boolean' ? got.pattern : DEFAULT.pattern,
      tint: typeof got.tint === 'number' && got.tint >= 0 && got.tint <= 1 ? got.tint : DEFAULT.tint,
      mascot: typeof got.mascot === 'boolean' ? got.mascot : DEFAULT.mascot,
      motion: typeof got.motion === 'boolean' ? got.motion : DEFAULT.motion,
      sky: typeof got.sky === 'boolean' ? got.sky : DEFAULT.sky,
      slim: typeof got.slim === 'boolean' ? got.slim : DEFAULT.slim,
    };
  } catch {
    return DEFAULT;
  }
}

// The theme marks the root element: the glass styles and their backdrop hang on it.
const mark = () => {
  document.documentElement.dataset.deckTheme = look.theme;
  document.documentElement.dataset.deckSurface = look.surface;
  document.documentElement.dataset.deckMotion = look.motion ? 'on' : 'off';
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

// fonts are the faces of the themes that have their own: juce Helvetica, pixel monospace, paper with serifs.
const fonts: Partial<Record<Look['theme'], string>> = {
  juce: '"Helvetica Neue", Helvetica, Arial, system-ui, -apple-system, sans-serif',
  pixel: 'ui-monospace, "SF Mono", Menlo, Consolas, monospace',
  paper: 'ui-serif, "New York", Charter, Georgia, serif',
};

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
    defaultRadius: l.theme === 'pixel' ? 0 : l.radius,
    spacing: scaled(DEFAULT_THEME.spacing, space),
    fontSizes: scaled(DEFAULT_THEME.fontSizes, text),
    ...(fonts[l.theme] ? { fontFamily: fonts[l.theme], headings: { sizes: headings, fontFamily: fonts[l.theme] } } : { headings: { sizes: headings } }),
    components: l.theme === 'glass' ? glass : {},
  });
}
