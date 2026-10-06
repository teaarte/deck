import { Group, Loader, Text } from '@mantine/core';
import { useTranslation } from 'react-i18next';
import type { ChatValue } from '../lib/catalog.gen';
import { Actions } from '../ui/Actions';
import s from './chat.module.css';

// Turn is the reply going on: who replies or what it waits for, its last tools, and its buttons.
export function Turn({ turn }: { turn: NonNullable<ChatValue['turn']> }) {
  const { t } = useTranslation();
  return (
    <div className={s.turn} data-turn={turn.state}>
      <Group justify="space-between" wrap="nowrap">
        <Group gap="xs" wrap="nowrap" miw={0}>
          {turn.state === 'running' && <Loader size="xs" type="dots" />}
          <Text size="xs" c="dimmed" truncate>{turn.state === 'running' ? t('chat.replying', { provider: turn.provider ?? '' }) : t('chat.waits.' + turn.state, { defaultValue: turn.state })}</Text>
        </Group>
        <Actions actions={[turn.stop!, turn.open!].filter(Boolean)} />
      </Group>
      {turn.lines.filter((l) => l.kind === 'tool').slice(-4).map((l, i) => (
        <Text key={i} size="xs" ff="monospace" c="dimmed" truncate data-turn-line>{l.title}</Text>
      ))}
    </div>
  );
}
