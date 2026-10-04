import type { ReactNode } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { Alert, Badge, Card, Group, Loader, Select, Stack, Text, Title } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { IconAlertTriangle, IconInbox } from '@tabler/icons-react';
import type { Data } from './lib/catalog.gen';
import s from './shell.module.css';
import { NamedIcon } from './ui/NamedIcon';
import { useLive } from './lib/events';

type Filter = NonNullable<Data['filters']>[number];

// Filters narrow a block by the values its source has; the pick goes back to the server with the screen.
function Filters({ filters, context }: { filters?: Filter[]; context?: ComponentContext }) {
  const { t } = useTranslation();
  if (!filters?.length || !context) return null;
  const pick = (field: string, value: string | null) =>
    void context.dispatchAction({ event: { name: 'filter', context: { block: context.componentModel.id, field, value: value ?? '' } } });
  return (
    <Group gap="xs">
      {filters.map((f) => (
        <Select key={f.field} size="xs" w={180} clearable placeholder={t('filter.any', { field: f.label ?? f.field })} data={f.options}
          value={f.value || null} onChange={(v) => pick(f.field, v)} data-filter={f.field} />
      ))}
    </Group>
  );
}

export type Span = 1 | 2 | 'full';

export function Block({ title, icon, span, description, data, context, children }: { title?: string; icon?: string; span?: Span; description?: string; data?: Data; context?: ComponentContext; children: (d: Data) => ReactNode }) {
  const { t } = useTranslation();
  const old = !useLive();
  let body: ReactNode;
  if (!data) body = <Group gap="xs"><Loader size="xs" /><Text size="sm" c="dimmed">{t('state.loading')}</Text></Group>;
  else if (data.state === 'error') body = <Alert color="red" radius="md" icon={<IconAlertTriangle size={18} />} title={t('state.error')}>{data.error}</Alert>;
  else if (data.state === 'empty') {
    body = (
      <Group gap={8} c="dimmed" wrap="nowrap">
        <IconInbox size={18} stroke={1.5} />
        <Text size="sm" c="dimmed" data-empty>{data.empty || t('state.empty')}</Text>
      </Group>
    );
  }
  else body = children(data);
  return (
    <Card padding="lg" className={s.block} data-block data-span={String(span ?? 1)}>
      <Stack gap="sm">
        {(title || old) && (
          <Group justify="space-between">
            {title ? (
              <Title order={3} className={s.blockTitle}>
                {icon && <span className={s.titleIcon} data-block-icon><NamedIcon name={icon} fallback={title} size={18} /></span>}{title}
              </Title>
            ) : <span />}
            {old && <Badge color="yellow" variant="light" title={t('state.stale_why')} data-stale>{t('state.stale')}</Badge>}
          </Group>
        )}
        {description && <Text size="sm" c="dimmed" mt={title ? -6 : 0} lh={1.5} data-description>{description}</Text>}
        <Filters filters={data?.filters} context={context} />
        {body}
      </Stack>
    </Card>
  );
}
