import { lazy, Suspense, useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import { z } from 'zod';
import { Catalog, type ComponentApi, type ComponentContext } from '@a2ui/web_core/v0_9';
import { createComponentImplementation, type ReactComponentImplementation } from '@a2ui/react/v0_9';
import {
  Alert, Anchor, Badge, Button, Card, Chip, Code, Collapse, Group, Loader, Select, SimpleGrid, Stack, Table, Tabs, Text,
  TextInput, Title, UnstyledButton,
} from '@mantine/core';
import { useTranslation } from 'react-i18next';
import catalogSchema from '../../schemas/catalog.json';
import { Board, type BoardValue } from './board';
import type { CustomValue } from './custom';
import { Form, type FormValue } from './form';

const Custom = lazy(() => import('./custom').then((m) => ({ default: m.Custom })));

export const CATALOG_ID = catalogSchema.catalogId;
export const UNKNOWN = 'deck.unknown';

export interface Action {
  label?: string;
  verb?: string;
  primary?: boolean;
  command?: string;
  params?: Record<string, unknown>;
  input?: string;
  until?: string;
  project?: string;
  screen?: string;
  record?: number;
}

interface Question {
  question: string;
  multiSelect?: boolean;
  options?: { label: string; description?: string }[];
}

interface Filter {
  field: string;
  value?: string;
  options: string[];
}

interface Data {
  state: 'ready' | 'empty' | 'error';
  error?: string;
  filters?: Filter[];
  rows?: { id: unknown; cells: unknown[]; actions?: Action[] }[];
  items?: { id: unknown; title: string; text?: string; meta?: string; details?: string; questions?: Question[]; actions?: Action[] }[];
  value?: unknown;
  lines?: { at: string; kind: string; id?: string; title?: string; status?: string; text?: string }[];
}

// outcomes holds whether the call behind each pressed button went through, by the ticket the button sent.
export const outcomes = new Map<string, boolean>();
let tickets = 0;

let stale = false;
const staleSubs = new Set<() => void>();
export function setStale(v: boolean) {
  stale = v;
  staleSubs.forEach((f) => f());
}
const useStale = () => useSyncExternalStore((f) => (staleSubs.add(f), () => staleSubs.delete(f)), () => stale);

const useNow = () => {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 15000);
    return () => clearInterval(t);
  }, []);
  return now;
};

const moment = /^\d{4}-\d\d-\d\dT\d\d:\d\d(:\d\d(\.\d+)?)?(Z|[+-]\d\d:\d\d)$/;

function show(v: unknown): string {
  if (v === null || v === undefined) return '';
  if (typeof v === 'object') return JSON.stringify(v);
  if (typeof v === 'string' && moment.test(v)) return new Date(v).toLocaleString();
  return String(v);
}

function ActionButton({ act, context, params }: { act: Action; context: ComponentContext; params?: Record<string, unknown> }) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const running = useRef(false);
  const [typing, setTyping] = useState(false);
  const [text, setText] = useState('');
  const label = act.label || (act.verb ? t('verb.' + act.verb) : '');
  const send = async (extra?: Record<string, unknown>) => {
    if (running.current) return;
    running.current = true;
    setBusy(true);
    try {
      const all = { ...act.params, ...params, ...extra };
      const ticket = String(++tickets);
      await context.dispatchAction({ event: { name: act.screen ? 'open' : 'command', context: { ...act, params: all, ticket } } });
      const ok = outcomes.get(ticket) !== false;
      outcomes.delete(ticket);
      if (ok) {
        setTyping(false);
        setText('');
      }
    } finally {
      running.current = false;
      setBusy(false);
    }
  };
  if (act.input && !params) {
    if (!typing) {
      return <Button size="xs" variant={act.primary ? 'filled' : 'default'} onClick={() => setTyping(true)}>{label}</Button>;
    }
    return (
      <Group gap="xs" wrap="nowrap">
        <TextInput size="xs" autoFocus placeholder={t('input.' + act.input, { defaultValue: '' })} value={text}
          onChange={(e) => setText(e.currentTarget.value)}
          onKeyDown={(e) => { if (e.key === 'Enter' && text.trim()) void send({ [act.input!]: text }); }} />
        <Button size="xs" loading={busy} disabled={!text.trim()} onClick={() => send({ [act.input!]: text })}>{t('verb.send')}</Button>
        <Button size="xs" variant="subtle" onClick={() => setTyping(false)}>{t('verb.cancel')}</Button>
      </Group>
    );
  }
  return (
    <Button size="xs" variant={act.primary ? 'filled' : 'default'} loading={busy} onClick={() => send()}>{label}</Button>
  );
}

function Actions({ actions, context }: { actions?: Action[]; context: ComponentContext }) {
  const now = useNow();
  const shown = (actions ?? []).filter((a) => !a.until || Date.parse(a.until) > now);
  if (!shown.length) return null;
  return <Group gap="xs">{shown.map((a, i) => <ActionButton key={i} act={a} context={context} />)}</Group>;
}

function QuestionPicker({ questions, act, context }: { questions: Question[]; act?: Action; context: ComponentContext }) {
  const { t } = useTranslation();
  const [picked, setPicked] = useState<Record<string, string | string[]>>({});
  const [other, setOther] = useState<Record<string, string>>({});
  const answer = (q: Question) => {
    const o = other[q.question]?.trim();
    const p = picked[q.question];
    if (q.multiSelect) return [...((p as string[]) ?? []), ...(o ? [o] : [])].join(', ');
    return o || (p as string) || '';
  };
  const ready = questions.every((q) => answer(q) !== '');
  if (!act) return null;
  const all = Object.fromEntries(questions.map((q) => [q.question, answer(q)]));
  return (
    <Stack gap="xs">
      {questions.map((q) => (
        <Stack key={q.question} gap={4}>
          <Text size="sm" fw={500}>{q.question}</Text>
          {q.multiSelect ? (
            <Chip.Group multiple value={(picked[q.question] as string[]) ?? []} onChange={(v) => setPicked({ ...picked, [q.question]: v })}>
              <Group gap="xs">{(q.options ?? []).map((o) => <Chip key={o.label} value={o.label} size="xs">{o.label}</Chip>)}</Group>
            </Chip.Group>
          ) : (
            <Chip.Group multiple={false} value={(picked[q.question] as string) ?? ''} onChange={(v) => setPicked({ ...picked, [q.question]: v })}>
              <Group gap="xs">{(q.options ?? []).map((o) => <Chip key={o.label} value={o.label} size="xs">{o.label}</Chip>)}</Group>
            </Chip.Group>
          )}
          <TextInput size="xs" placeholder={t('question.other')} value={other[q.question] ?? ''}
            onChange={(e) => setOther({ ...other, [q.question]: e.currentTarget.value })} />
        </Stack>
      ))}
      <Group>
        {ready ? <ActionButton act={act} context={context} params={{ [act.input ?? 'answer']: JSON.stringify(all) }} />
          : <Button size="xs" disabled>{act.label || t('verb.' + (act.verb ?? 'answer'))}</Button>}
      </Group>
    </Stack>
  );
}

// Filters narrow a block by the values its source has; the pick goes back to the server with the screen.
function Filters({ filters, context }: { filters?: Filter[]; context?: ComponentContext }) {
  const { t } = useTranslation();
  if (!filters?.length || !context) return null;
  const pick = (field: string, value: string | null) =>
    void context.dispatchAction({ event: { name: 'filter', context: { block: context.componentModel.id, field, value: value ?? '' } } });
  return (
    <Group gap="xs">
      {filters.map((f) => (
        <Select key={f.field} size="xs" w={180} clearable placeholder={t('filter.any', { field: f.field })} data={f.options}
          value={f.value || null} onChange={(v) => pick(f.field, v)} data-filter={f.field} />
      ))}
    </Group>
  );
}

function Block({ title, data, context, children }: { title?: string; data?: Data; context?: ComponentContext; children: (d: Data) => ReactNode }) {
  const { t } = useTranslation();
  const old = useStale();
  let body: ReactNode;
  if (!data) body = <Group gap="xs"><Loader size="xs" /><Text size="sm" c="dimmed">{t('state.loading')}</Text></Group>;
  else if (data.state === 'error') body = <Alert color="red" title={t('state.error')}>{data.error}</Alert>;
  else if (data.state === 'empty') body = <Text size="sm" c="dimmed">{t('state.empty')}</Text>;
  else body = children(data);
  return (
    <Card withBorder padding="md" data-block>
      <Stack gap="sm">
        {(title || old) && (
          <Group justify="space-between">
            {title ? <Title order={4}>{title}</Title> : <span />}
            {old && <Badge color="yellow" variant="light" title={t('state.stale_why')} data-stale>{t('state.stale')}</Badge>}
          </Group>
        )}
        <Filters filters={data?.filters} context={context} />
        {body}
      </Stack>
    </Card>
  );
}

function Details({ text }: { text: string }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  return (
    <Stack gap={4} align="flex-start">
      <Anchor component="button" size="xs" onClick={() => setOpen(!open)}>{open ? t('details.hide') : t('details.show')}</Anchor>
      <Collapse expanded={open} w="100%"><Code block>{text}</Code></Collapse>
    </Stack>
  );
}

const loaded = Catalog.fromSchema(catalogSchema);
const api = (name: string): ComponentApi => {
  const c = loaded.components.get(name);
  if (!c) throw new Error('catalog has no ' + name);
  return c;
};

type Children = (string | { id: string; basePath?: string })[];
const kids = (children: unknown, build: (id: string, base?: string) => ReactNode) =>
  ((Array.isArray(children) ? children : []) as Children).map((c, i) =>
    typeof c === 'string' ? <div key={c}>{build(c)}</div> : <div key={c.id + i}>{build(c.id, c.basePath)}</div>);

const impls: ReactComponentImplementation[] = [
  createComponentImplementation(api('screen'), ({ props, buildChild }) => {
    const p = props as { title?: string; children: unknown };
    return <Stack gap="md">{p.title && <Title order={2}>{p.title}</Title>}{kids(p.children, buildChild)}</Stack>;
  }),
  createComponentImplementation(api('columns'), ({ props, buildChild }) => {
    const p = props as { title?: string; children: unknown[] };
    return (
      <Stack gap="xs">
        {p.title && <Title order={4}>{p.title}</Title>}
        <SimpleGrid cols={{ base: 1, md: Math.max(1, (p.children ?? []).length) }}>{kids(p.children, buildChild)}</SimpleGrid>
      </Stack>
    );
  }),
  createComponentImplementation(api('tabs'), ({ props, buildChild }) => {
    const p = props as { title?: string; titles: string[]; children: unknown[] };
    const ids = (p.children ?? []) as Children;
    return (
      <Stack gap="xs">
      {p.title && <Title order={4}>{p.title}</Title>}
      <Tabs defaultValue="0">
        <Tabs.List>{p.titles.map((title, i) => <Tabs.Tab key={i} value={String(i)}>{title}</Tabs.Tab>)}</Tabs.List>
        {ids.map((c, i) => (
          <Tabs.Panel key={i} value={String(i)} pt="sm">{typeof c === 'string' ? buildChild(c) : buildChild(c.id, c.basePath)}</Tabs.Panel>
        ))}
      </Tabs>
      </Stack>
    );
  }),
  createComponentImplementation(api('table'), ({ props, context }) => {
    const p = props as { title?: string; columns: string[]; data?: Data };
    return (
      <Block title={p.title} data={p.data} context={context}>
        {(d) => (
          <Table.ScrollContainer minWidth={400}>
            <Table striped highlightOnHover>
              <Table.Thead>
                <Table.Tr>{p.columns.map((c) => <Table.Th key={c}>{c}</Table.Th>)}{d.rows?.some((r) => r.actions?.length) && <Table.Th />}</Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {(d.rows ?? []).map((r, i) => (
                  <Table.Tr key={show(r.id) || i}>
                    {r.cells.map((v, j) => <Table.Td key={j}>{show(v)}</Table.Td>)}
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
    const p = props as { title?: string; data?: Data };
    return (
      <Block title={p.title} data={p.data} context={context}>
        {(d) => (
          <Stack gap="sm">
            {(d.items ?? []).map((it, i) => {
              const answer = it.questions ? it.actions?.find((a) => a.input) : undefined;
              const rest = (it.actions ?? []).filter((a) => a !== answer);
              return (
                <Stack key={show(it.id) || i} gap={6} data-item>
                  <Text fw={500}>{it.title}</Text>
                  {it.text && <Text size="sm">{it.text}</Text>}
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
    const p = props as { title?: string; data?: Data };
    return (
      <Block title={p.title} data={p.data}>
        {(d) => {
          const v = (d.value ?? {}) as { fields?: { label: string; value: unknown }[]; actions?: Action[] };
          return (
            <Stack gap="xs">
              {(v.fields ?? []).map((f) => (
                <Group key={f.label} gap="sm" wrap="nowrap" align="flex-start">
                  <Text size="sm" c="dimmed" w={120}>{f.label}</Text>
                  <Text size="sm">{show(f.value)}</Text>
                </Group>
              ))}
              <Actions actions={v.actions} context={context} />
            </Stack>
          );
        }}
      </Block>
    );
  }),
  createComponentImplementation(api('stat'), ({ props }) => {
    const p = props as { title?: string; data?: Data };
    return <Block title={p.title} data={p.data}>{(d) => <Text fz={32} fw={700}>{show(d.value)}</Text>}</Block>;
  }),
  createComponentImplementation(api('actions'), ({ props, context }) => {
    const p = props as { title?: string; actions: Action[] };
    return <Block title={p.title} data={{ state: 'ready' }}>{() => <Actions actions={p.actions} context={context} />}</Block>;
  }),
  createComponentImplementation(api('log'), ({ props }) => {
    const { t } = useTranslation();
    const p = props as { title?: string; data?: Data };
    return (
      <Block title={p.title} data={p.data}>
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
  createComponentImplementation(api('board'), ({ props }) => {
    const p = props as { title?: string; data?: Data };
    return <Block title={p.title} data={p.data}>{(d) => <Board value={d.value as BoardValue} />}</Block>;
  }),
  createComponentImplementation(api('form'), ({ props }) => {
    const p = props as { title?: string; data?: Data };
    return <Block title={p.title} data={p.data}>{(d) => <Form value={d.value as FormValue} />}</Block>;
  }),
  createComponentImplementation(api('custom'), ({ props }) => {
    const p = props as { title?: string; data?: Data };
    return <Block title={p.title} data={p.data}>{(d) => <Suspense fallback={<Loader size="xs" />}><Custom value={d.value as CustomValue} /></Suspense>}</Block>;
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
