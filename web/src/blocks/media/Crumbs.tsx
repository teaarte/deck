import { ActionIcon, Anchor, Breadcrumbs, Group, Text } from '@mantine/core';
import { IconArrowLeft } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { Action, Data } from '../../lib/catalog.gen';
import { usePress } from '../../ui/actions/usePress';

export type Crumb = NonNullable<Data['crumbs']>[number];

const none: Action = {};

function Step({ c }: { c: Crumb }) {
  const { send, busy } = usePress(c.action ?? none);
  if (!c.action) return <Text size="sm" fw={500} truncate data-crumb="here">{c.label}</Text>;
  return <Anchor component="button" size="sm" disabled={busy} onClick={() => void send()} truncate data-crumb>{c.label}</Anchor>;
}

function Up({ c }: { c: Crumb }) {
  const { t } = useTranslation();
  const { send, busy } = usePress(c.action ?? none);
  return (
    <ActionIcon variant="default" size={30} disabled={busy} onClick={() => void send()} aria-label={t('files.up')} data-crumb-up>
      <IconArrowLeft size={16} />
    </ActionIcon>
  );
}

// Crumbs are the steps of the folder a file manager shows, each opening its folder, and a step up.
export function Crumbs({ crumbs }: { crumbs: Crumb[] }) {
  return (
    <Group gap="xs" wrap="nowrap" style={{ minWidth: 0, flex: 1 }} data-crumbs>
      <Up c={crumbs[crumbs.length - 2]} />
      <Breadcrumbs separator="›" separatorMargin={6} style={{ minWidth: 0, flexWrap: 'wrap' }}>
        {crumbs.map((c, i) => <Step key={i} c={c} />)}
      </Breadcrumbs>
    </Group>
  );
}
