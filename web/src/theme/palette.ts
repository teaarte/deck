import type { CSSVariablesResolver, MantineColorsTuple } from '@mantine/core';
import palette from '../../../schemas/palette.json';

export { palette };

// colors are the saturated accents of deck in OKLCH: deep ones take white text on shade 6, bright ones black (autoContrast).
export const colors = palette.colors as unknown as Record<string, MantineColorsTuple>;

export const resolver: CSSVariablesResolver = () => ({
  variables: {},
  light: Object.fromEntries(Object.entries(palette.textShade).flatMap(([c, k]) => [
    [`--mantine-color-${c}-text`, `var(--mantine-color-${c}-${k})`], [`--mantine-color-${c}-outline`, `var(--mantine-color-${c}-${k})`]])),
  dark: {},
});
