import { useContext, useRef, useState, type ReactNode } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { Alert, Anchor, Badge, Card, Group, Stack, Text, UnstyledButton } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import { IconAlertTriangle, IconChevronRight, IconInbox } from '@tabler/icons-react';
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

// Block is the frame of a block: title, head buttons, icon, description, filters, state, own look of nothing, how many
// items its limit left out; a collapsed one is its title until a press opens it, then with how many it holds.
export function Block({ title, icon, span, description, collapsed, data, context, empty, bare, children }: {
  title?: string; icon?: string; span?: Span; description?: string; collapsed?: boolean; data?: Data; context?: ComponentContext;
  empty?: (d: Data) => ReactNode; bare?: Bare; children: (d: Data) => ReactNode;
}) {
  const [open, setOpen] = useState(!collapsed);
  // The first open reads the block and keeps it read with the screen from then on, though data cached may show already.
  const asked = useRef(false);
  const { t } = useTranslation();
  const old = !useLive();
  // A title the tab or the dialog around says already is not said again.
  const named = title !== useContext(Around) ? title : undefined;
  const small = useContext(Small);
  const fit = useContext(Fit);
  tally('block');
  let body: ReactNode;
  const own = data?.state === 'empty' && !fit ? empty?.(data) : undefined;
  // An empty block the server says is of no use stands out of sight.
  if (data?.state === 'empty' && data.quiet && !fit) return null;
  if (!data) body = <Pending text={t('Loading…')} />;
  else if (data.state === 'error' && fit) {
    body = <Group gap={8} wrap="nowrap" c="red" className={s.fitLine} title={data.error}><IconAlertTriangle size={18} /><Text size="sm" truncate>{t('The source failed')}</Text></Group>;
  }
  else if (data.state === 'error') body = <Alert color="red" icon={<IconAlertTriangle size={18} />} title={t('The source failed')}>{data.error}</Alert>;
  else if (own) body = own;
  else if (data.state === 'empty') {
    body = (
      <Group gap={8} c="dimmed" wrap="nowrap" className={fit ? s.fitLine : undefined}>
        <IconInbox size={18} stroke={1.5} />
        <Text size="sm" c="dimmed" truncate={fit} data-empty>{data.empty || t('Nothing here')}</Text>
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
  if (collapsed && title) {
    const count = data?.items?.length ?? data?.rows?.length;
    const head = (
      <UnstyledButton className={s.fold} aria-expanded={open} data-fold={open ? 'open' : 'closed'} onClick={() => {
        if (!open && !asked.current && context) {
          asked.current = true;
          readBlock(context, 'unfold');
        }
        setOpen(!open);
      }}>
        <IconChevronRight size={18} className={s.foldChevron} />
        <IconTitle order={3} className={s.blockTitle} title={named ?? title} icon={icon} size={18} block />
        {count !== undefined && data?.state !== 'error' && <Badge variant="light" color="gray" data-fold-count>{count}{data?.more ? '+' : ''}</Badge>}
      </UnstyledButton>
    );
    if (!open) return <Card padding="lg" className={s.block} data-block data-collapsed data-span={String(span ?? 1)}>{head}</Card>;
    return (
      <Card padding="lg" className={s.block} data-block data-collapsed="open" data-span={String(span ?? 1)}>
        <Stack gap="sm">
          {head}
          {description && <Text size="sm" c="dimmed" lh={1.5} data-description>{description}</Text>}
          <Filters filters={data?.filters} context={context} />
          {body}
        </Stack>
      </Card>
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
              {old && <Badge color="yellow" variant="light" title={t('The live connection is down; this may be old')} data-stale>{t('Out of date')}</Badge>}
            </Group>
          </Group>
        )}
        {description && <Text size="sm" c="dimmed" mt={title ? -6 : 0} lh={1.5} data-description>{description}</Text>}
        <Filters filters={data?.filters} context={context} />
        {body}
        {!!data?.more && data.state === 'ready' && (context
          ? <Anchor component="button" size="xs" onClick={() => readBlock(context, 'more')} data-more={data.more}>{t('Show more · {{count}} left', { count: data.more })}</Anchor>
          : <Text size="xs" c="dimmed" data-more={data.more}>{t('{{count}} more not shown', { count: data.more })}</Text>)}
      </Stack>
    </Card>
  );
}
