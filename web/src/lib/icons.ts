import { useSyncExternalStore } from 'react';

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
export const useNodes = () => useSyncExternalStore((f) => (subs.add(f), () => subs.delete(f)), () => nodes ?? (failed ? null : undefined));

// svgOf is the markup of an icon for a drawn pattern.
export function svgOf(icon: Node[]): string {
  return icon.map(([tag, attrs]) => '<' + tag + Object.entries(attrs).filter(([k]) => k !== 'key')
    .map(([k, v]) => ` ${k}="${v}"`).join('') + '/>').join('');
}
