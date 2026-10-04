import { createElement, useEffect, useSyncExternalStore } from 'react';
import { Box } from '@mantine/core';

type Node = [string, Record<string, string>];
export type Nodes = Record<string, Node[]>;

// The whole outline set of Tabler Icons loads once, when the backdrop draws or a manifest names an icon;
// the package exports only its SVG files, so the set is read by its path.
let nodes: Nodes | undefined;
let failed = false;
let loading: Promise<Nodes> | undefined;
const subs = new Set<() => void>();
export function loadIcons(): Promise<Nodes> {
  loading ??= import('../../node_modules/@tabler/icons/tabler-nodes-outline.json').then((m) => {
    nodes = m.default as unknown as Nodes;
    return nodes;
  }, (e) => {
    failed = true;
    loading = undefined;
    throw e;
  }).finally(() => subs.forEach((f) => f()));
  return loading;
}
const useNodes = () => useSyncExternalStore((f) => (subs.add(f), () => subs.delete(f)), () => nodes ?? (failed ? null : undefined));

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

// svgOf is the markup of an icon for a drawn pattern.
export function svgOf(icon: Node[]): string {
  return icon.map(([tag, attrs]) => '<' + tag + Object.entries(attrs).filter(([k]) => k !== 'key')
    .map(([k, v]) => ` ${k}="${v}"`).join('') + '/>').join('');
}
