import { createComponentImplementation } from '@a2ui/react/v0_9';
import { TabsApi } from '@a2ui/web_core/v0_9';
import { createContext, useContext } from 'react';
import { Badge, Tabs } from '@mantine/core';
import { setParam, useRoute } from '../app/router';
import { useMenuData, type Pulse } from '../hooks/useMenu';
import { NamedIcon } from '../ui/NamedIcon';
import { Around } from './around';

// COUNTS are the counts of deck a tab may show beside its title, from the pulse of the menu, kept fresh.
const COUNTS: Record<string, (p: Pulse) => number> = { failures: (p) => (p.failed ?? 0) + (p.errors ?? 0) };

// Inner is set inside a tab: tabs there keep their pick in a parameter of their own.
const Inner = createContext(false);

// tabs shows one of its children at a time; the tab picked is in the address by the key of its child.
export const tabs = createComponentImplementation(TabsApi, ({ props, buildChild, context }) => {
  const param = useContext(Inner) ? 'sub' : 'tab';
  const all = props.tabs ?? [];
  const panel = (id: string) => (context.surfaceComponents.get(id)?.properties ?? {}) as { key?: string; icon?: string; count?: string };
  const { pulse } = useMenuData();
  const keys = all.map((t) => panel(t.child).key ?? String(t.title));
  const route = useRoute();
  const at = Math.max(0, keys.indexOf(route.params?.[param] ?? ''));
  return (
    <div data-span="full">
      <Tabs value={String(at)} onChange={(v) => setParam(param, v === '0' ? null : keys[Number(v)])} keepMounted={false}>
        <Tabs.List>{all.map((t, i) => {
          const { icon, count } = panel(t.child);
          const n = count && COUNTS[count] ? COUNTS[count](pulse) : 0;
          return (
            <Tabs.Tab key={i} value={String(i)} leftSection={icon ? <NamedIcon name={icon} fallback={String(t.title)} size={16} /> : undefined}
              rightSection={n > 0 ? <Badge size="sm" circle color="red" data-tab-count={count}>{n}</Badge> : undefined}>{String(t.title)}</Tabs.Tab>
          );
        })}</Tabs.List>
        <Inner.Provider value>
          {all.map((t, i) => <Tabs.Panel key={i} value={String(i)} pt="sm"><Around.Provider value={String(t.title)}>{buildChild(t.child)}</Around.Provider></Tabs.Panel>)}
        </Inner.Provider>
      </Tabs>
    </div>
  );
});
