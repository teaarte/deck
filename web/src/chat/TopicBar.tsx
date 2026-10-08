import { ActionIcon, Group, Indicator, Menu, Select, Tooltip } from '@mantine/core';
import { IconCheck, IconPlus, IconStack2 } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { ChatValue } from '../lib/catalog.gen';
import { ActionsMenu } from '../ui/ActionsMenu';
import type { useTopics } from './useTopics';

// TopicBar picks the topic of the chat, narrows the topics to a module, starts a new one, and holds its buttons; it
// shrinks to the width it has.
export function TopicBar({ value, topics }: { value: ChatValue; topics: ReturnType<typeof useTopics> }) {
  const { t } = useTranslation();
  const topic = value.topic;
  const narrowed = topics.scopes.find((x) => x.value === topics.scope && topics.scope !== topics.scopes[0]?.value);
  return (
    <Group gap={4} wrap="nowrap" miw={0}>
      <Select size="xs" radius="xl" style={{ flex: 1, minWidth: 0 }} placeholder={t('chat.new_topic')} data={topics.options} value={topic ? String(topic.id) : null}
        onChange={(v) => topics.pick(v ?? 'new')} data-topics searchable nothingFoundMessage={t('chat.no_topics')} comboboxProps={{ width: 'max-content', position: 'bottom-start', styles: { dropdown: { maxWidth: 'calc(100vw - 16px)' } } }} />
      {topics.scopes.length > 0 && (
        <Indicator disabled={!narrowed} size={7} offset={5}>
          <Menu position="bottom-end" shadow="md" radius="md" withinPortal>
            <Tooltip label={narrowed ? `${t('chat.module')}: ${narrowed.label}` : t('chat.module')}>
              <Menu.Target>
                <ActionIcon variant={narrowed ? 'light' : 'subtle'} color={narrowed ? undefined : 'gray'} radius="xl" size={28}
                  aria-label={t('chat.module')} data-topic-project={topics.scope}>
                  <IconStack2 size={15} />
                </ActionIcon>
              </Menu.Target>
            </Tooltip>
            <Menu.Dropdown data-topic-projects>
              {topics.scopes.map((x) => (
                <Menu.Item key={x.value} onClick={() => topics.setScope(x.value)} data-scope={x.value}
                  rightSection={x.value === topics.scope ? <IconCheck size={14} /> : undefined}>{x.label}</Menu.Item>
              ))}
            </Menu.Dropdown>
          </Menu>
        </Indicator>
      )}
      <Tooltip label={t('chat.new_topic')}>
        <ActionIcon variant="subtle" color="gray" radius="xl" size={28} onClick={() => topics.pick('new')} aria-label={t('chat.new_topic')} data-new-topic><IconPlus size={16} /></ActionIcon>
      </Tooltip>
      {topic && <ActionsMenu actions={[value.archive!, value.delete!].filter(Boolean)} />}
    </Group>
  );
}
