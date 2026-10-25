import { Avatar, Badge, createTheme, DEFAULT_THEME, Switch, type MantineThemeOverride } from '@mantine/core';
import { colors, palette } from './palette';
import type { Worn } from './worn';
import { createStore, useStore } from '../lib/store';

// Look is what this device keeps of the look: the theme itself is the whole deck's, and the color scheme Mantine's.
export interface Look {
  accent: string;
  density: 'compact' | 'default' | 'comfortable';
  font: 'small' | 'default' | 'large';
  // mascot shows Deki in the chat and on the cockpit; motion keeps the animations.
  mascot: boolean;
  motion: boolean;
}

const KEY = 'deck-look';
export const ACCENTS = palette.accents;
export const DENSITIES: Look['density'][] = ['compact', 'default', 'comfortable'];
export const FONTS: Look['font'][] = ['small', 'default', 'large'];
const DEFAULT: Look = { accent: 'coral', density: 'default', font: 'default', mascot: true, motion: true };

function read(): Look {
  try {
    const got = JSON.parse(localStorage.getItem(KEY) ?? '{}') as Partial<Look>;
    return {
      accent: ACCENTS.includes(got.accent ?? '') ? got.accent! : DEFAULT.accent,
      density: DENSITIES.includes(got.density!) ? got.density! : DEFAULT.density,
      font: FONTS.includes(got.font!) ? got.font! : DEFAULT.font,
      mascot: typeof got.mascot === 'boolean' ? got.mascot : DEFAULT.mascot,
      motion: typeof got.motion === 'boolean' ? got.motion : DEFAULT.motion,
    };
  } catch {
    return DEFAULT;
  }
}

// The motion marks the root element: base.css stops the animations by it.
const mark = (l: Look) => {
  document.documentElement.dataset.deckMotion = l.motion ? 'on' : 'off';
};
const look = createStore(read());
mark(look.get());
const show = (l: Look) => {
  mark(l);
  look.set(l);
};
export function setLook(change: Partial<Look>) {
  const l = { ...look.get(), ...change };
  localStorage.setItem(KEY, JSON.stringify(l));
  show(l);
}
window.addEventListener('storage', (e) => {
  if (e.key === KEY) show(read());
});
export const useLook = () => useStore(look);

const scaled = (sizes: Record<string, string>, by: number) =>
  Object.fromEntries(Object.entries(sizes).map(([k, v]) => [k, by === 1 ? v : `calc(${v} * ${by})`]));

// The faces are of the system, none in the build: text and code.
const SYSTEM = 'system-ui, -apple-system, "Segoe UI", Roboto, sans-serif';
const MONO = 'ui-monospace, "SF Mono", Menlo, Consolas, monospace';
const SPACE = { compact: 0.75, default: 1, comfortable: 1.25 };
// SQUARE are the parts Mantine rounds by its own pill radius, squared in a theme of radius 0.
const SQUARE = {
  Avatar: Avatar.extend({ defaultProps: { radius: 0 } }),
  Badge: Badge.extend({ defaultProps: { radius: 0 } }),
  Switch: Switch.extend({ defaultProps: { radius: 0 } }),
};

// theme is the Mantine theme of the worn theme and this device: the accent is the primary color, the density of the
// device scales the theme's, font the text.
export function theme(l: Look, w: Worn): MantineThemeOverride {
  const space = SPACE[l.density] * SPACE[w.density];
  const text = { small: 0.9, default: 1, large: 1.15 }[l.font];
  const headings = Object.fromEntries(Object.entries(DEFAULT_THEME.headings.sizes).map(([k, h]) =>
    [k, { ...h, fontSize: text === 1 ? h.fontSize : `calc(${h.fontSize} * ${text})` }]));
  return createTheme({
    primaryColor: l.accent,
    colors,
    primaryShade: { light: 6, dark: 6 },
    autoContrast: true,
    respectReducedMotion: true,
    defaultRadius: w.radius === '0' ? 0 : w.radius,
    // A theme of radius 0 squares every radius of deck, the round ones too.
    ...(w.radius === '0' ? { radius: { xs: '0', sm: '0', md: '0', lg: '0', xl: '0' }, components: SQUARE } : {}),
    spacing: scaled(DEFAULT_THEME.spacing, space),
    fontSizes: scaled(DEFAULT_THEME.fontSizes, text),
    fontFamily: w.font.text || SYSTEM,
    fontFamilyMonospace: MONO,
    headings: { sizes: headings, fontFamily: w.font.headings || w.font.text || SYSTEM },
  });
}
