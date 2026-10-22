import { createStore, useStore } from './store';

type Node = [string, Record<string, string>];
export type Nodes = Record<string, Node[]>;

// The outline set of Tabler Icons loads once, when the backdrop draws or a manifest names an icon, by its path.
// nodes is null while the last load failed.
const nodes = createStore<Nodes | null | undefined>(undefined);
let loading: Promise<Nodes> | undefined;
export function loadIcons(): Promise<Nodes> {
  loading ??= import('../../node_modules/@tabler/icons/tabler-nodes-outline.json').then((m) => {
    const got = m.default as unknown as Nodes;
    nodes.set(got);
    return got;
  }, (e) => {
    loading = undefined;
    nodes.set(null);
    throw e;
  });
  return loading;
}
export const useNodes = () => useStore(nodes);

// svgOf is the markup of an icon for a drawn pattern.
export function svgOf(icon: Node[]): string {
  return icon.map(([tag, attrs]) => '<' + tag + Object.entries(attrs).filter(([k]) => k !== 'key')
    .map(([k, v]) => ` ${k}="${v}"`).join('') + '/>').join('');
}
