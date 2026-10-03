import { lazy, Suspense, type ReactNode } from 'react';
import { z } from 'zod';
import { Catalog, type ComponentApi } from '@a2ui/web_core/v0_9';
import { createComponentImplementation, type ReactComponentImplementation } from '@a2ui/react/v0_9';
import { Alert, Badge, Button, Code, Group, Loader, Modal, SimpleGrid, Stack, Table, Tabs, Text, Title, UnstyledButton } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import catalogSchema from '../../schemas/catalog.json';
import s from './shell.module.css';
import { NamedIcon } from './icons';
import { dropParam, setParam, useRoute } from './router';
import { DialogClose } from './dialog';
import { Board } from './board';
import { Form, type FormValue } from './form';
import { Document, Markdown } from './document';
import { Actions, QuestionPicker, rowOpen } from './actions';
import { Block, Details, show } from './block';
import { Appearance } from './appearance';
import { Chat } from './chat';
import { toneColor, Value, type Shown } from './format';
import type { Action, ActionsValue, BoardValue, CardValue, ChartValue, ChatValue, CustomValue, Data, DocumentValue } from './catalog.gen';

const Custom = lazy(() => import('./custom').then((m) => ({ default: m.Custom })));
const Media = lazy(() => import('./media').then((m) => ({ default: m.Media })));
const Chart = lazy(() => import('./chart').then((m) => ({ default: m.Chart })));

export const CATALOG_ID = catalogSchema.catalogId;
export const UNKNOWN = 'deck.unknown';

export type { Action };

const loaded = Catalog.fromSchema(catalogSchema);
const api = (name: string): ComponentApi => {
  const c = loaded.components.get(name);
  if (!c) throw new Error('catalog has no ' + name);
  return c;
};

type Children = (string | { id: string; basePath?: string })[];
const kids = (children: unknown, build: (id: string, base?: string) => ReactNode, className?: string) =>
  ((Array.isArray(children) ? children : []) as Children).map((c, i) =>
    typeof c === 'string' ? <div key={c} className={className}>{build(c)}</div> : <div key={c.id + i} className={className}>{build(c.id, c.basePath)}</div>);

const impls: ReactComponentImplementation[] = [
  createComponentImplementation(api('screen'), ({ props, buildChild }) => {
    const p = props as { title?: string; icon?: string; children: unknown };
    return (
      <Stack gap="lg" data-screen>
        {p.title && (
          <Title order={2} lh={1.2} lts={-0.4}>
            {p.icon && <span className={s.titleIcon}><NamedIcon name={p.icon} fallback={p.title} size={22} /></span>}{p.title}
          </Title>
        )}
        {kids(p.children, buildChild)}
      </Stack>
    );
  }),
  createComponentImplementation(api('columns'), ({ props, buildChild }) => {
    const p = props as { title?: string; icon?: string; children: unknown[] };
    const n = Math.max(1, (p.children ?? []).length);
    return (
      <Stack gap="xs">
        {p.title && <Title order={4}>{p.title}</Title>}
        <SimpleGrid cols={{ base: 1, sm: Math.min(2, n), lg: n }} spacing="lg" verticalSpacing="lg" data-columns>
          {kids(p.children, buildChild, s.cell)}
        </SimpleGrid>
      </Stack>
    );
  }),
  createComponentImplementation(api('tabs'), ({ props, buildChild }) => {
    const p = props as { title?: string; icon?: string; titles: string[]; children: unknown[] };
    const ids = (p.children ?? []) as Children;
    const route = useRoute();
    const at = Math.max(0, p.titles.indexOf(route.params?.tab ?? ''));
    return (
      <Stack gap="xs">
      {p.title && <Title order={4}>{p.title}</Title>}
      <Tabs value={String(at)} onChange={(v) => setParam('tab', v === '0' ? null : p.titles[Number(v)])}>
        <Tabs.List>{p.titles.map((title, i) => <Tabs.Tab key={i} value={String(i)}>{title}</Tabs.Tab>)}</Tabs.List>
        {ids.map((c, i) => (
          <Tabs.Panel key={i} value={String(i)} pt="sm">{typeof c === 'string' ? buildChild(c) : buildChild(c.id, c.basePath)}</Tabs.Panel>
        ))}
      </Tabs>
      </Stack>
    );
  }),
  createComponentImplementation(api('dialog'), ({ props, buildChild }) => {
    const p = props as { title?: string; icon?: string; name: string; primary?: boolean; children: unknown };
    const route = useRoute();
    const open = route.params?.dialog === p.name;
    const close = () => dropParam('dialog');
    return (
      <>
        <Button variant={p.primary ? 'filled' : 'default'} radius="xl" data-dialog-open={p.name} onClick={() => setParam('dialog', p.name)}
          leftSection={p.icon ? <NamedIcon name={p.icon} fallback={p.title ?? ''} size={16} /> : undefined}>{p.title}</Button>
        <Modal opened={open} onClose={close} size="lg" radius="lg" data-dialog={p.name} className={s.dialog}
          title={<Group gap={8}>{p.icon && <NamedIcon name={p.icon} fallback={p.title ?? ''} size={20} />}<Text fw={700} size="lg">{p.title}</Text></Group>}>
          <DialogClose.Provider value={close}>
            {open && <Stack gap="md">{kids(p.children, buildChild)}</Stack>}
          </DialogClose.Provider>
        </Modal>
      </>
    );
  }),
  createComponentImplementation(api('table'), ({ props, context }) => {
    const p = props as { title?: string; icon?: string; columns: (Shown & { label: string })[]; data?: Data };
    return (
      <Block title={p.title} icon={p.icon} data={p.data} context={context}>
        {(d) => (
          <Table.ScrollContainer minWidth={400}>
            <Table striped highlightOnHover>
              <Table.Thead>
                <Table.Tr>{p.columns.map((c, i) => <Table.Th key={i}>{c.label}</Table.Th>)}{d.rows?.some((r) => r.actions?.length) && <Table.Th />}</Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {(d.rows ?? []).map((r, i) => (
                  <Table.Tr key={show(r.id) || i} {...rowOpen(r.actions, context)}>
                    {r.cells.map((v, j) => <Table.Td key={j}><Value v={v} s={p.columns[j] ?? {}} tone={r.tones?.[j]} /></Table.Td>)}
                    {!!r.actions?.length && <Table.Td><Actions actions={r.actions} context={context} /></Table.Td>}
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        )}
      </Block>
    );
  }),
  createComponentImplementation(api('list'), ({ props, context }) => {
    const p = props as { title?: string; icon?: string; data?: Data };
    return (
      <Block title={p.title} icon={p.icon} data={p.data} context={context}>
        {(d) => (
          <Stack gap="sm">
            {(d.items ?? []).map((it, i) => {
              const answer = it.questions ? it.actions?.find((a) => a.input) : undefined;
              const rest = (it.actions ?? []).filter((a) => a !== answer);
              const opens = rowOpen(it.actions, context);
              return (
                <Stack key={show(it.id) || i} gap={6} data-item data-tone={it.tone} {...opens}
                  style={{ ...opens.style, ...(it.tone && it.tone !== 'muted' ? { borderLeft: `3px solid var(--mantine-color-${toneColor(it.tone)}-6)`, paddingLeft: 'var(--mantine-spacing-xs)' } : {}) }}
                  opacity={it.tone === 'muted' ? 0.6 : undefined}>
                  <Text fw={500}>{it.title}</Text>
                  {it.text && (it.markdown ? <Markdown text={it.text} /> : <Text size="sm">{it.text}</Text>)}
                  {it.meta && <Text size="xs" c="dimmed">{it.meta}</Text>}
                  {it.details && <Details text={it.details} />}
                  {it.questions && <QuestionPicker questions={it.questions} act={answer} context={context} />}
                  <Actions actions={rest} context={context} />
                </Stack>
              );
            })}
          </Stack>
        )}
      </Block>
    );
  }),
  createComponentImplementation(api('card'), ({ props, context }) => {
    const p = props as { title?: string; icon?: string; data?: Data };
    return (
      <Block title={p.title} icon={p.icon} data={p.data}>
        {(d) => {
          const v = (d.value ?? {}) as Partial<CardValue>;
          return (
            <Stack gap="xs">
              {(v.fields ?? []).map((f) => (
                f.markdown && typeof f.value === 'string' ? (
                  <Stack key={f.label} gap={2}>
                    <Text size="sm" c="dimmed">{f.label}</Text>
                    <Markdown text={f.value} />
                  </Stack>
                ) : (
                  <Group key={f.label} gap="sm" wrap="nowrap" align="flex-start">
                    <Text size="sm" c="dimmed" w={120}>{f.label}</Text>
                    <Text size="sm" component="div"><Value v={f.value} s={f} tone={f.tone} /></Text>
                  </Group>
                )
              ))}
              <Actions actions={v.actions} context={context} />
            </Stack>
          );
        }}
      </Block>
    );
  }),
  createComponentImplementation(api('stat'), ({ props }) => {
    const p = props as Shown & { title?: string; icon?: string; data?: Data };
    return <Block title={p.title} icon={p.icon} data={p.data}>{(d) => <Text fz={32} fw={700} component="div" data-stat><Value v={d.value} s={p} tone={d.tone} /></Text>}</Block>;
  }),
  createComponentImplementation(api('chart'), ({ props }) => {
    const p = props as Shown & { title?: string; icon?: string; kind?: string; data?: Data };
    return (
      <Block title={p.title} icon={p.icon} data={p.data}>
        {(d) => <Suspense fallback={<Loader size="xs" />}><div data-chart-block><Chart value={d.value as ChartValue} kind={p.kind} s={p} /></div></Suspense>}
      </Block>
    );
  }),
  createComponentImplementation(api('media'), ({ props }) => {
    const p = props as { title?: string; icon?: string; data?: Data };
    return <Block title={p.title} icon={p.icon} data={p.data}>{(d) => <Suspense fallback={<Loader size="xs" />}><Media items={d.files ?? []} /></Suspense>}</Block>;
  }),
  createComponentImplementation(api('actions'), ({ props, context }) => {
    const p = props as { title?: string; icon?: string; data?: Data };
    return <Block title={p.title} icon={p.icon} data={p.data}>{(d) => <Actions actions={d.value as ActionsValue | undefined} context={context} />}</Block>;
  }),
  createComponentImplementation(api('log'), ({ props }) => {
    const { t } = useTranslation();
    const p = props as { title?: string; icon?: string; data?: Data };
    return (
      <Block title={p.title} icon={p.icon} data={p.data}>
        {(d) => {
          const done = new Map((d.lines ?? []).filter((l) => l.kind === 'tool_update' && l.id).map((l) => [l.id, l]));
          return (
            <Stack gap={6}>
              {(d.lines ?? []).filter((l) => l.kind !== 'tool_update' || !l.id).map((l, i) => {
                const end = l.kind === 'tool' && l.id ? done.get(l.id) : undefined;
                return (
                  <Stack key={i} gap={2} data-line={l.kind}>
                    <Group gap="xs">
                      <Badge size="xs" variant="light" color={end?.status === 'failed' ? 'red' : 'gray'}>{t('log.' + l.kind, { defaultValue: l.kind })}</Badge>
                      {l.title && <Text size="xs" ff="monospace">{l.title}</Text>}
                      <Text size="xs" c="dimmed">{new Date(l.at).toLocaleTimeString()}</Text>
                    </Group>
                    {l.text && <Text size="sm" c={l.kind === 'thought' ? 'dimmed' : undefined} style={{ whiteSpace: 'pre-wrap' }} lineClamp={l.kind === 'message' ? undefined : 4}>{l.text}</Text>}
                    {end?.text && <Code block>{end.text.slice(0, 1000)}</Code>}
                  </Stack>
                );
              })}
            </Stack>
          );
        }}
      </Block>
    );
  }),
  createComponentImplementation(api('board'), ({ props, context }) => {
    const p = props as { title?: string; icon?: string; data?: Data };
    return <Block title={p.title} icon={p.icon} data={p.data}>{(d) => <Board value={d.value as BoardValue} context={context} />}</Block>;
  }),
  createComponentImplementation(api('form'), ({ props }) => {
    const p = props as { title?: string; icon?: string; data?: Data };
    return <Block title={p.title} icon={p.icon} data={p.data}>{(d) => <Form value={d.value as FormValue} />}</Block>;
  }),
  createComponentImplementation(api('custom'), ({ props }) => {
    const p = props as { title?: string; icon?: string; data?: Data };
    return <Block title={p.title} icon={p.icon} data={p.data}>{(d) => <Suspense fallback={<Loader size="xs" />}><Custom value={d.value as CustomValue} /></Suspense>}</Block>;
  }),
  createComponentImplementation(api('document'), ({ props }) => {
    const p = props as { title?: string; icon?: string; data?: Data };
    return <Block title={p.title} icon={p.icon} data={p.data}>{(d) => <Document value={d.value as DocumentValue} />}</Block>;
  }),
  createComponentImplementation(api('chat'), ({ props, context }) => {
    const p = props as { title?: string; icon?: string; data?: Data };
    return <Block title={p.title} icon={p.icon} data={p.data}>{(d) => <Chat value={d.value as ChatValue} context={context} />}</Block>;
  }),
  createComponentImplementation(api('appearance'), ({ props }) => {
    const p = props as { title?: string; icon?: string; data?: Data };
    return <Block title={p.title} icon={p.icon} data={p.data}>{() => <Appearance />}</Block>;
  }),
  createComponentImplementation({ name: UNKNOWN, schema: z.object({ type: z.string() }) }, ({ props }) => {
    const { t } = useTranslation();
    return (
      <Alert color="gray" data-unknown={props.type}>
        <Stack gap="xs" align="flex-start">
          <Text size="sm">{t('screen.unknown', { type: props.type })}</Text>
          <UnstyledButton component="a" href={location.href} onClick={() => location.reload()}>
            <Text c="blue" size="sm">{t('screen.open_in_web')}</Text>
          </UnstyledButton>
        </Stack>
      </Alert>
    );
  }),
];

export const catalog = new Catalog<ReactComponentImplementation>(CATALOG_ID, impls);
export const known = new Set(impls.map((c) => c.name));
