import { lazy, Suspense, type ReactNode } from 'react';
import { z } from 'zod';
import { Catalog, type ComponentApi, type ComponentContext } from '@a2ui/web_core/v0_9';
import { createComponentImplementation, type ReactComponentImplementation } from '@a2ui/react/v0_9';
import { Alert, Badge, Button, Code, Group, Loader, Modal, SimpleGrid, Stack, Tabs, Text, Title, UnstyledButton } from '@mantine/core';
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
import { Block, Details, show, type Span } from './block';
import { DataTable } from './table';
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

type BlockProps = { title?: string; icon?: string; span?: Span; description?: string; data?: Data };

// blockImpl draws a block of the catalog in its frame: title, icon, span, description, filters and state.
function blockImpl<P = object>(name: string, body: (p: BlockProps & P, d: Data, context: ComponentContext) => ReactNode) {
  return createComponentImplementation(api(name), ({ props, context }) => {
    const p = props as BlockProps & P;
    return <Block title={p.title} icon={p.icon} span={p.span} description={p.description} data={p.data} context={context}>{(d) => body(p, d, context)}</Block>;
  });
}

function LogLines({ lines }: { lines: NonNullable<Data['lines']> }) {
  const { t } = useTranslation();
  const done = new Map(lines.filter((l) => l.kind === 'tool_update' && l.id).map((l) => [l.id, l]));
  return (
    <Stack gap={6}>
      {lines.filter((l) => l.kind !== 'tool_update' || !l.id).map((l, i) => {
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
}

type Children = (string | { id: string; basePath?: string })[];
const kids = (children: unknown, build: (id: string, base?: string) => ReactNode, className?: string) =>
  ((Array.isArray(children) ? children : []) as Children).map((c, i) =>
    typeof c === 'string' ? <div key={c} className={className}>{build(c)}</div> : <div key={c.id + i} className={className}>{build(c.id, c.basePath)}</div>);

const impls: ReactComponentImplementation[] = [
  createComponentImplementation(api('screen'), ({ props, buildChild }) => {
    const p = props as { title?: string; icon?: string; span?: Span; description?: string; children: unknown; dialogs?: unknown };
    const dialogs = Array.isArray(p.dialogs) && p.dialogs.length > 0;
    return (
      <Stack gap="lg" data-screen data-span="full">
        {(p.title || dialogs) && (
          <Group justify="space-between" align="center" gap="sm" data-screen-head>
            {p.title ? (
              <Title order={2} className={s.screenTitle}>
                {p.icon && <span className={s.titleIcon}><NamedIcon name={p.icon} fallback={p.title} size={24} /></span>}{p.title}
              </Title>
            ) : <span />}
            {dialogs && <Group gap="xs">{kids(p.dialogs, buildChild)}</Group>}
          </Group>
        )}
        <div className={s.grid} data-grid>{kids(p.children, buildChild, s.cell)}</div>
      </Stack>
    );
  }),
  createComponentImplementation(api('columns'), ({ props, buildChild }) => {
    const p = props as { title?: string; icon?: string; span?: Span; description?: string; children: unknown[] };
    const n = Math.max(1, (p.children ?? []).length);
    return (
      <Stack gap="xs" data-span={String(p.span ?? 'full')}>
        {p.title && <Title order={3} className={s.blockTitle}>{p.title}</Title>}
        <SimpleGrid cols={{ base: 1, sm: Math.min(2, n), lg: n }} spacing="lg" verticalSpacing="lg" data-columns>
          {kids(p.children, buildChild, s.cell)}
        </SimpleGrid>
      </Stack>
    );
  }),
  createComponentImplementation(api('tabs'), ({ props, buildChild }) => {
    const p = props as { title?: string; icon?: string; span?: Span; description?: string; titles: string[]; keys?: string[]; icons?: string[]; children: unknown[] };
    const ids = (p.children ?? []) as Children;
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
  }),
  createComponentImplementation(api('dialog'), ({ props, buildChild }) => {
    const p = props as { title?: string; icon?: string; span?: Span; description?: string; name: string; primary?: boolean; children: unknown };
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
  blockImpl<{ columns: (Shown & { label: string })[] }>('table', (p, d, context) => <DataTable columns={p.columns} rows={d.rows ?? []} context={context} />),
  blockImpl('list', (_, d, context) => (
    <Stack gap="sm">
      {(d.items ?? []).map((it, i) => {
        const answer = it.questions ? it.actions?.find((a) => a.input) : undefined;
        const rest = (it.actions ?? []).filter((a) => a !== answer);
        const opens = rowOpen(it.actions, context);
        return (
          <Stack key={show(it.id) || i} gap={6} data-item data-tone={it.tone} {...opens}
            style={{ ...opens.style, ...(it.tone && it.tone !== 'muted' ? { borderLeft: `3px solid var(--mantine-color-${toneColor(it.tone)}-6)`, paddingLeft: 'var(--mantine-spacing-xs)' } : {}) }}
            opacity={it.tone === 'muted' ? 0.6 : undefined}>
            <Group gap={10} wrap="nowrap" align="flex-start">
              {it.icon && <span className={s.itemIcon}><NamedIcon name={it.icon} fallback={it.title} size={18} /></span>}
              <Stack gap={2} miw={0} style={{ flex: 1 }}>
                <Text fw={600} size="md" lh={1.35} className={s.itemTitle}>{it.title}</Text>
                {it.text && (it.markdown ? <Markdown text={it.text} /> : <Text size="sm" c="var(--deck-text-2)" lh={1.5}>{it.text}</Text>)}
                {it.meta && <Text size="xs" c="dimmed" className={s.meta}>{it.meta}</Text>}
              </Stack>
            </Group>
            {it.details && <Details text={it.details} />}
            {it.questions && <QuestionPicker questions={it.questions} act={answer} context={context} />}
            <Actions actions={rest} context={context} />
          </Stack>
        );
      })}
    </Stack>
  )),
  blockImpl('card', (_, d, context) => {
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
  }),
  blockImpl<Shown>('stat', (p, d) => <Text fz={32} fw={700} component="div" data-stat><Value v={d.value} s={p} tone={d.tone} /></Text>),
  blockImpl<Shown & { kind?: string }>('chart', (p, d) => (
    <Suspense fallback={<Loader size="xs" />}><div data-chart-block><Chart value={d.value as ChartValue} kind={p.kind} s={p} /></div></Suspense>
  )),
  blockImpl('media', (_, d) => <Suspense fallback={<Loader size="xs" />}><Media items={d.files ?? []} /></Suspense>),
  blockImpl('actions', (_, d, context) => <Actions actions={d.value as ActionsValue | undefined} context={context} />),
  blockImpl('log', (_, d) => <LogLines lines={d.lines ?? []} />),
  blockImpl('board', (_, d, context) => <Board value={d.value as BoardValue} context={context} />),
  blockImpl('form', (_, d) => <Form value={d.value as FormValue} />),
  blockImpl('custom', (_, d) => <Suspense fallback={<Loader size="xs" />}><Custom value={d.value as CustomValue} /></Suspense>),
  blockImpl('document', (_, d) => <Document value={d.value as DocumentValue} />),
  blockImpl('chat', (_, d, context) => <Chat value={d.value as ChatValue} context={context} />),
  blockImpl<{ part?: string }>('appearance', (p) => <Appearance part={p.part} />),
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
