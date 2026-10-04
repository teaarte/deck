import type { ReactNode } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { Alert, Badge, Card, Group, Loader, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { IconAlertTriangle, IconInbox } from '@tabler/icons-react';
import type { Data } from '../lib/catalog.gen';
import s from '../shell.module.css';
import { IconTitle } from '../ui/IconTitle';
import { useLive } from '../lib/events';
import { Filters } from './Filters';

export type Span = 1 | 2 | 'full';

// Block is the frame of a block: its title, icon, description, filters, and the state of its data.
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
              <IconTitle order={3} className={s.blockTitle} title={title} icon={icon} size={18} block />
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
