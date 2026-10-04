import type { CSSVariablesResolver, MantineColorsTuple } from '@mantine/core';

// palette holds saturated accents in OKLCH: deep ones take white text on shade 6, bright ones black (autoContrast).
export const palette: Record<string, MantineColorsTuple> = {
  blue: ['#f2f7fe', '#dfecfe', '#bfdafe', '#98c4ff', '#70adfe', '#4998fe', '#0173e6', '#0365cb', '#0252a7', '#004085'],
  indigo: ['#f4f6fe', '#e5eafe', '#ccd5fe', '#afbdfe', '#93a3ff', '#7c8cfe', '#5b62f9', '#4c4ee4', '#3c35cb', '#2f17b3'],
  violet: ['#f7f5ff', '#ece8fe', '#dad0ff', '#c6b3ff', '#b296ff', '#a37bfe', '#8f4cff', '#7a2de5', '#6702cb', '#5101a1'],
  grape: ['#fdf3ff', '#f9e2fe', '#f3c4fe', '#ec9efe', '#e573ff', '#d852f5', '#bd33da', '#a203be', '#85009d', '#69007c'],
  pink: ['#fef4f7', '#fee3eb', '#fec5d7', '#ffa0c0', '#fe76aa', '#fe4498', '#df1c7e', '#bd0468', '#9c0255', '#7b0142'],
  red: ['#fff4f3', '#ffe4e1', '#fec8c3', '#fea69e', '#ff7e77', '#ff5352', '#e81c2e', '#c50220', '#a20118', '#810010'],
  orange: ['#fef5f0', '#ffeade', '#fedbc6', '#ffcbac', '#ffbb92', '#feaa76', '#ff9550', '#d76902', '#a75001', '#7d3a01'],
  yellow: ['#fff7de', '#ffeebb', '#ffe185', '#ffd337', '#f4c707', '#e7bc0d', '#d8b00a', '#a98904', '#826901', '#614e00'],
  lime: ['#ecffd7', '#dbfcb4', '#c3f779', '#adf129', '#a2e506', '#99d80b', '#8fca07', '#6e9e05', '#547a02', '#3e5a01'],
  green: ['#e5ffe9', '#c8ffd2', '#95ffad', '#53fd89', '#43f17e', '#30e572', '#0dd766', '#01a84d', '#00823a', '#016129'],
  teal: ['#e0fff8', '#bdfef0', '#86fce6', '#38f7db', '#1debd0', '#0bdfc4', '#10d0b8', '#01a38f', '#017e6e', '#005d51'],
  cyan: ['#e9fbfe', '#d0f7ff', '#acf0fe', '#80eaff', '#3fe3ff', '#0dd8f5', '#02cae6', '#0b9db3', '#02798b', '#035a67'],
  gray: ['#f5f7fa', '#e8ebf1', '#d2d8e2', '#bac1ce', '#a4abb8', '#9199a5', '#6f7682', '#626975', '#4f5661', '#3c434d'],
  dark: ['#e6e6ec', '#c6c6cf', '#9d9daa', '#70707d', '#474752', '#35353e', '#27272e', '#1b1b21', '#121216', '#0a0a0d'],
};

// textShade is the shade of a bright color that its text and outlines take on white: 4.5:1 or more.
const textShade: Record<string, number> = { orange: 8, yellow: 8, lime: 8, green: 8, teal: 8, cyan: 8 };

export const resolver: CSSVariablesResolver = () => ({
  variables: {},
  light: Object.fromEntries(Object.entries(textShade).flatMap(([c, k]) => [
    [`--mantine-color-${c}-text`, `var(--mantine-color-${c}-${k})`], [`--mantine-color-${c}-outline`, `var(--mantine-color-${c}-${k})`]])),
  dark: {},
});
