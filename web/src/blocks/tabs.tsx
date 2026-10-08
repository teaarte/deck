import { createComponentImplementation } from '@a2ui/react/v0_9';
import { createContext, useContext } from 'react';
import { Stack, Tabs, Title } from '@mantine/core';
import s from './blocks.module.css';
import { setParam, useRoute } from '../app/router';
import { NamedIcon } from '../ui/NamedIcon';
import type { Span } from './Block';
import { api, children } from './blockImpl';

// Inner is set inside a tab: tabs there keep their pick in a parameter of their own.
const Inner = createContext(false);

// tabs shows one of its children at a time; the tab picked is in the address by its key.
export const tabs = createComponentImplementation(api('tabs'), ({ props, buildChild }) => {
  const param = useContext(Inner) ? 'sub' : 'tab';
  const p = props as { title?: string; span?: Span; titles: string[]; keys?: string[]; icons?: string[]; children: unknown[] };
  const ids = children(p.children);
  const keys = p.keys ?? p.titles;
  const route = useRoute();
  const at = Math.max(0, keys.indexOf(route.params?.[param] ?? ''));
  return (
    <Stack gap="xs" data-span={String(p.span ?? 'full')}>
      {p.title && <Title order={3} className={s.blockTitle}>{p.title}</Title>}
      <Tabs value={String(at)} onChange={(v) => setParam(param, v === '0' ? null : keys[Number(v)])}>
        <Tabs.List>{p.titles.map((title, i) => (
          <Tabs.Tab key={i} value={String(i)} leftSection={p.icons?.[i] ? <NamedIcon name={p.icons[i]} fallback={title} size={16} /> : undefined}>{title}</Tabs.Tab>
        ))}</Tabs.List>
        <Inner.Provider value>
          {ids.map((c, i) => (
            <Tabs.Panel key={i} value={String(i)} pt="sm">{typeof c === 'string' ? buildChild(c) : buildChild(c.id, c.basePath)}</Tabs.Panel>
          ))}
        </Inner.Provider>
      </Tabs>
    </Stack>
  );
});
