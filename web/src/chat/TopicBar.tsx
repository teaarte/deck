import { ActionIcon, Group, Select, Tooltip } from '@mantine/core';
import { IconPlus } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { ChatValue } from '../lib/catalog.gen';
import { ActionsMenu } from '../ui/ActionsMenu';
import type { useTopics } from './useTopics';

// TopicBar picks the topic of the chat, narrows the topics to a module, starts a new one, and holds its buttons.
export function TopicBar({ value, topics }: { value: ChatValue; topics: ReturnType<typeof useTopics> }) {
  const { t } = useTranslation();
  const topic = value.topic;
  return (
    <Group gap={6} wrap="nowrap">
      <Select size="xs" radius="xl" style={{ flex: 1 }} placeholder={t('chat.new_topic')} data={topics.options} value={topic ? String(topic.id) : null}
        onChange={(v) => topics.pick(v ?? 'new')} data-topics searchable nothingFoundMessage={t('chat.no_topics')} />
      {topics.scopes.length > 0 && (
        <Select size="xs" radius="xl" w={140} allowDeselect={false} data-topic-project aria-label={t('chat.module')}
          data={topics.scopes} value={topics.scope} onChange={topics.setScope} />
      )}
      <Tooltip label={t('chat.new_topic')}>
        <ActionIcon variant="default" radius="xl" size={30} onClick={() => topics.pick('new')} aria-label={t('chat.new_topic')} data-new-topic><IconPlus size={16} /></ActionIcon>
      </Tooltip>
      {topic && <ActionsMenu actions={[value.archive!, value.delete!].filter(Boolean)} />}
    </Group>
  );
}
