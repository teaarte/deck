import { createElement, useEffect } from 'react';
import { Box } from '@mantine/core';
import { loadIcons, useNodes } from '../lib/icons';

// NamedIcon draws a Tabler icon by its name; an unknown name, or none, shows the first letter of the fallback.
export function NamedIcon({ name, fallback, size = 18, stroke = 1.75 }: { name?: string; fallback: string; size?: number; stroke?: number }) {
  const all = useNodes();
  useEffect(() => { if (name && all === undefined) loadIcons().catch(() => undefined); }, [name, all]);
  const icon = name ? all?.[name] : undefined;
  if (!icon) {
    return (
      <Box component="span" w={size} h={size} fz={size * 0.62} fw={700} data-icon-letter
        style={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'center', lineHeight: 1, textTransform: 'uppercase' }}>
        {name && all === undefined ? '' : fallback.slice(0, 1)}
      </Box>
    );
  }
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth={stroke} strokeLinecap="round" strokeLinejoin="round" data-icon={name} aria-hidden>
      {icon.map(([tag, { key, ...attrs }], i) => createElement(tag, { key: key ?? i, ...attrs }))}
    </svg>
  );
}
