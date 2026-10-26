import { useContext, type ReactNode } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { Alert, Anchor, Badge, Card, Group, Stack, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { IconAlertTriangle, IconInbox } from '@tabler/icons-react';
import type { Data } from '../lib/catalog.gen';
import s from './blocks.module.css';
import { IconTitle } from '../ui/IconTitle';
import { Pending } from '../ui/Pending';
import { useLive } from '../lib/events';
import { tally } from '../lib/tally';
import { readBlock } from '../screen/press';
import { Actions } from '../ui/actions/Actions';
import { Around, Fit, Small } from './around';
import { Filters } from './Filters';

export type Span = 1 | 2 | 'full';

// Bare is a block without its frame: in a row after its title, or plain with its title for screen readers alone.
export type Bare = 'row' | 'plain' | undefined;

// Block is the frame of a block: its title with the buttons of its head, icon, description, filters, the state of its
// data, its own look of nothing when it has one, and how many items its limit left out, which a press reads more of.
export function Block({ title, icon, span, description, data, context, empty, bare, children }: {
  title?: string; icon?: string; span?: Span; description?: string; data?: Data; context?: ComponentContext;
  empty?: (d: Data) => ReactNode; bare?: Bare; children: (d: Data) => ReactNode;
}) {
  const { t } = useTranslation();
  const old = !useLive();
  // A title the tab or the dialog around says already is not said again.
  const named = title !== useContext(Around) ? title : undefined;
  const small = useContext(Small);
  const fit = useContext(Fit);
  tally('block');
  let body: ReactNode;
  const own = data?.state === 'empty' ? empty?.(data) : undefined;
  if (!data) body = <Pending text={t('state.loading')} />;
  else if (data.state === 'error' && fit) {
    body = <Group gap={8} wrap="nowrap" c="red" className={s.fitLine} title={data.error}><IconAlertTriangle size={18} /><Text size="sm" truncate>{t('state.error')}</Text></Group>;
  }
  else if (data.state === 'error') body = <Alert color="red" icon={<IconAlertTriangle size={18} />} title={t('state.error')}>{data.error}</Alert>;
  else if (own) body = own;
  else if (data.state === 'empty') {
    body = (
      <Group gap={8} c="dimmed" wrap="nowrap" className={fit ? s.fitLine : undefined}>
        <IconInbox size={18} stroke={1.5} />
        <Text size="sm" c="dimmed" truncate={fit} data-empty>{data.empty || t('state.empty')}</Text>
      </Group>
    );
  }
  else body = children(data);
  if (small) {
    return (
      <section className={s.small} aria-label={title} data-block data-small data-fit={fit || undefined} data-span={String(span ?? 1)}>
        {named && <Text size="xs" fw={700} c="dimmed" className={s.bareTitle} truncate={fit}>{named}</Text>}
        {body}
      </section>
    );
  }
  if (bare === 'plain') return <section className={s.bare} aria-label={title} data-block data-bare="plain" data-span={String(span ?? 1)}>{body}</section>;
  if (bare === 'row') {
    return (
      <section className={s.bareRow} aria-label={title} data-block data-bare="row" data-span={String(span ?? 1)}>
        {named && <Text size="sm" fw={700} c="dimmed" className={s.bareTitle}>{named}</Text>}
        {body}
      </section>
    );
  }
  return (
    <Card padding="lg" className={s.block} data-block data-span={String(span ?? 1)}>
      <Stack gap="sm">
        {(named || old || !!data?.head?.length) && (
          <Group justify="space-between">
            {named ? (
              <IconTitle order={3} className={s.blockTitle} title={named} icon={icon} size={18} block />
            ) : <span />}
            <Group gap="xs">
              {!!data?.head?.length && <Actions actions={data.head} quiet />}
              {old && <Badge color="yellow" variant="light" title={t('state.stale_why')} data-stale>{t('state.stale')}</Badge>}
            </Group>
          </Group>
        )}
        {description && <Text size="sm" c="dimmed" mt={title ? -6 : 0} lh={1.5} data-description>{description}</Text>}
        <Filters filters={data?.filters} context={context} />
        {body}
        {!!data?.more && data.state === 'ready' && (context
          ? <Anchor component="button" size="xs" onClick={() => readBlock(context, 'more')} data-more={data.more}>{t('state.show_more', { count: data.more })}</Anchor>
          : <Text size="xs" c="dimmed" data-more={data.more}>{t('state.more', { count: data.more })}</Text>)}
      </Stack>
    </Card>
  );
}
