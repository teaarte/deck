import { useState, useSyncExternalStore, type ReactNode } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { Alert, Anchor, Badge, Card, Code, Collapse, Group, Loader, Select, Stack, Text, Title } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { Data } from './catalog.gen';

type Filter = NonNullable<Data['filters']>[number];

let stale = false;
const staleSubs = new Set<() => void>();
export function setStale(v: boolean) {
  stale = v;
  staleSubs.forEach((f) => f());
}
const useStale = () => useSyncExternalStore((f) => (staleSubs.add(f), () => staleSubs.delete(f)), () => stale);

const moment = /^\d{4}-\d\d-\d\dT\d\d:\d\d(:\d\d(\.\d+)?)?(Z|[+-]\d\d:\d\d)$/;

export function show(v: unknown): string {
  if (v === null || v === undefined) return '';
  if (typeof v === 'object') return JSON.stringify(v);
  if (typeof v === 'string' && moment.test(v)) return new Date(v).toLocaleString();
  return String(v);
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

export function Block({ title, data, context, children }: { title?: string; data?: Data; context?: ComponentContext; children: (d: Data) => ReactNode }) {
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

export function Details({ text }: { text: string }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  return (
    <Stack gap={4} align="flex-start">
      <Anchor component="button" size="xs" onClick={() => setOpen(!open)}>{open ? t('details.hide') : t('details.show')}</Anchor>
      <Collapse expanded={open} w="100%"><Code block>{text}</Code></Collapse>
    </Stack>
  );
}
