import type { McpUiHostContext, McpUiStyles } from '@modelcontextprotocol/ext-apps/app-bridge';

// HOST_VARIABLES are the MCP Apps style variables by the Mantine variable whose resolved value they take.
const HOST_VARIABLES: Partial<Record<keyof McpUiStyles, string>> = {
  '--color-background-primary': '--mantine-color-body',
  '--color-background-secondary': '--mantine-color-default',
  '--color-background-tertiary': '--mantine-color-default-hover',
  '--color-background-inverse': '--mantine-color-text',
  '--color-background-info': '--mantine-primary-color-light',
  '--color-background-danger': '--mantine-color-red-light',
  '--color-background-success': '--mantine-color-green-light',
  '--color-background-warning': '--mantine-color-yellow-light',
  '--color-background-disabled': '--mantine-color-disabled',
  '--color-text-primary': '--mantine-color-text',
  '--color-text-secondary': '--mantine-color-dimmed',
  '--color-text-tertiary': '--mantine-color-placeholder',
  '--color-text-inverse': '--mantine-color-body',
  '--color-text-info': '--mantine-primary-color-light-color',
  '--color-text-danger': '--mantine-color-error',
  '--color-text-success': '--mantine-color-green-light-color',
  '--color-text-warning': '--mantine-color-yellow-light-color',
  '--color-text-disabled': '--mantine-color-disabled-color',
  '--color-border-primary': '--mantine-color-default-border',
  '--color-border-secondary': '--mantine-color-default-border',
  '--color-border-info': '--mantine-primary-color-filled',
  '--color-border-danger': '--mantine-color-error',
  '--color-ring-primary': '--mantine-primary-color-filled',
  '--font-sans': '--mantine-font-family',
  '--font-mono': '--mantine-font-family-monospace',
  '--font-text-xs-size': '--mantine-font-size-xs',
  '--font-text-sm-size': '--mantine-font-size-sm',
  '--font-text-md-size': '--mantine-font-size-md',
  '--font-text-lg-size': '--mantine-font-size-lg',
  '--font-heading-sm-size': '--mantine-h4-font-size',
  '--font-heading-md-size': '--mantine-h3-font-size',
  '--font-heading-lg-size': '--mantine-h2-font-size',
  '--font-heading-xl-size': '--mantine-h1-font-size',
  '--border-radius-xs': '--mantine-radius-xs',
  '--border-radius-sm': '--mantine-radius-sm',
  '--border-radius-md': '--mantine-radius-md',
  '--border-radius-lg': '--mantine-radius-lg',
  '--border-radius-xl': '--mantine-radius-xl',
  '--shadow-sm': '--mantine-shadow-sm',
  '--shadow-md': '--mantine-shadow-md',
  '--shadow-lg': '--mantine-shadow-lg',
};

// hostContext is the theme of deck as MCP Apps passes it: resolved values, since the frame has none of deck's CSS.
export function hostContext(theme: 'light' | 'dark'): McpUiHostContext {
  const css = getComputedStyle(document.documentElement);
  const variables = Object.fromEntries(Object.entries(HOST_VARIABLES)
    .map(([k, from]) => [k, css.getPropertyValue(from!).trim()]).filter(([, v]) => v)) as McpUiStyles;
  return { theme, styles: { variables }, displayMode: 'inline', platform: 'web' };
}
