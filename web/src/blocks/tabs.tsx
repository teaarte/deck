import { createComponentImplementation } from '@a2ui/react/v0_9';
import { TabsApi } from '@a2ui/web_core/v0_9';
import { createContext, useContext } from 'react';
import { Tabs } from '@mantine/core';
import { setParam, useRoute } from '../app/router';
import { NamedIcon } from '../ui/NamedIcon';

// Inner is set inside a tab: tabs there keep their pick in a parameter of their own.
const Inner = createContext(false);

// tabs shows one of its children at a time; the tab picked is in the address by the key of its child.
export const tabs = createComponentImplementation(TabsApi, ({ props, buildChild, context }) => {
  const param = useContext(Inner) ? 'sub' : 'tab';
  const all = props.tabs ?? [];
  const panel = (id: string) => (context.surfaceComponents.get(id)?.properties ?? {}) as { key?: string; icon?: string };
  const keys = all.map((t) => panel(t.child).key ?? String(t.title));
  const route = useRoute();
  const at = Math.max(0, keys.indexOf(route.params?.[param] ?? ''));
  return (
    <div data-span="full">
      <Tabs value={String(at)} onChange={(v) => setParam(param, v === '0' ? null : keys[Number(v)])} keepMounted={false}>
        <Tabs.List>{all.map((t, i) => {
          const icon = panel(t.child).icon;
          return <Tabs.Tab key={i} value={String(i)} leftSection={icon ? <NamedIcon name={icon} fallback={String(t.title)} size={16} /> : undefined}>{String(t.title)}</Tabs.Tab>;
        })}</Tabs.List>
        <Inner.Provider value>
          {all.map((t, i) => <Tabs.Panel key={i} value={String(i)} pt="sm">{buildChild(t.child)}</Tabs.Panel>)}
        </Inner.Provider>
      </Tabs>
    </div>
  );
});
