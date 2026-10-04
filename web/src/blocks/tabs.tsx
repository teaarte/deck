import { createComponentImplementation } from '@a2ui/react/v0_9';
import { Stack, Tabs, Title } from '@mantine/core';
import s from '../shell.module.css';
import { setParam, useRoute } from '../app/router';
import { NamedIcon } from '../ui/NamedIcon';
import type { Span } from './Block';
import { api, children } from './blockImpl';

// tabs shows one of its children at a time; the tab picked is in the address by its key.
export const tabs = createComponentImplementation(api('tabs'), ({ props, buildChild }) => {
  const p = props as { title?: string; span?: Span; titles: string[]; keys?: string[]; icons?: string[]; children: unknown[] };
  const ids = children(p.children);
  const keys = p.keys ?? p.titles;
  const route = useRoute();
  const at = Math.max(0, keys.indexOf(route.params?.tab ?? ''));
  return (
    <Stack gap="xs" data-span={String(p.span ?? 'full')}>
      {p.title && <Title order={3} className={s.blockTitle}>{p.title}</Title>}
      <Tabs value={String(at)} onChange={(v) => setParam('tab', v === '0' ? null : keys[Number(v)])}>
        <Tabs.List>{p.titles.map((title, i) => (
          <Tabs.Tab key={i} value={String(i)} leftSection={p.icons?.[i] ? <NamedIcon name={p.icons[i]} fallback={title} size={16} /> : undefined}>{title}</Tabs.Tab>
        ))}</Tabs.List>
        {ids.map((c, i) => (
          <Tabs.Panel key={i} value={String(i)} pt="sm">{typeof c === 'string' ? buildChild(c) : buildChild(c.id, c.basePath)}</Tabs.Panel>
        ))}
      </Tabs>
    </Stack>
  );
});
