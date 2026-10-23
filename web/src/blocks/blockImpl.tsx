import type { ReactNode } from 'react';
import { Catalog, type ComponentApi, type ComponentContext } from '@a2ui/web_core/v0_9';
import { createComponentImplementation } from '@a2ui/react/v0_9';
import catalogSchema from '../lib/catalogSchema';
import type { Data } from '../lib/catalog.gen';
import { BlockContext } from '../screen/press';
import { Block, type Span } from './Block';

const loaded = Catalog.fromSchema(catalogSchema);

// api is the component of the catalog by its name.
export const api = (name: string): ComponentApi => {
  const c = loaded.components.get(name);
  if (!c) throw new Error('catalog has no ' + name);
  return c;
};

export type BlockProps = { title?: string; icon?: string; span?: Span; description?: string; data?: Data };

// blockImpl draws a block of the catalog in its frame: title, icon, span, description, filters and state; the buttons
// inside press through its A2UI context.
export function blockImpl<P = object>(name: string, body: (p: BlockProps & P, d: Data, context: ComponentContext) => ReactNode) {
  return createComponentImplementation(api(name), ({ props, context }) => {
    const p = props as BlockProps & P;
    return (
      <BlockContext.Provider value={context}>
        <Block title={p.title} icon={p.icon} span={p.span} description={p.description} data={p.data} context={context}>{(d) => body(p, d, context)}</Block>
      </BlockContext.Provider>
    );
  });
}

type Children = (string | { id: string; basePath?: string })[];

// children are the ids of a container's components, plain or with the path of their data.
export const children = (v: unknown): Children => (Array.isArray(v) ? v : []) as Children;

// kids draws the children of a container, each in a cell of className.
export const kids = (v: unknown, build: (id: string, base?: string) => ReactNode, className?: string) =>
  children(v).map((c, i) =>
    typeof c === 'string' ? <div key={c} className={className}>{build(c)}</div> : <div key={c.id + i} className={className}>{build(c.id, c.basePath)}</div>);
