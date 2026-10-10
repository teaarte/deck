import { ActionIcon, Group, Indicator, Menu, Select, Tooltip } from '@mantine/core';
import { IconCheck, IconPlus, IconStack2 } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { ChatValue } from '../lib/catalog.gen';
import n from '../app/nav.module.css';
import { ActionsMenu } from '../ui/actions/ActionsMenu';
import type { useTopics } from './useTopics';

// TopicBar picks the topic of the chat, narrows the topics to a module, starts a new one, and holds its buttons; it
// shrinks to the width it has. A pulsing dot marks the topics and modules where work goes on.
export function TopicBar({ value, topics }: { value: ChatValue; topics: ReturnType<typeof useTopics> }) {
  const { t } = useTranslation();
  const topic = value.topic;
  const narrowed = topics.scopes.find((x) => x.value === topics.scope && topics.scope !== topics.scopes[0]?.value);
  const pulse = <span className={n.pulse} data-busy />;
  const elsewhere = topics.working.size > 0;
  return (
    <Group gap={4} wrap="nowrap" miw={0}>
      <Select size="xs" radius="xl" style={{ flex: 1, minWidth: 0 }} placeholder={t('chat.new_topic')} data={topics.options} value={topic ? String(topic.id) : null}
        onChange={(v) => topics.pick(v ?? 'new')} data-topics
        leftSection={topic && topics.busy.has(String(topic.id)) ? pulse : undefined} leftSectionWidth={22}
        renderOption={({ option, checked }) => (
          <Group gap={8} wrap="nowrap">{checked && <IconCheck size={14} />}{option.label}{topics.busy.has(option.value) && pulse}</Group>
        )} searchable nothingFoundMessage={t('chat.no_topics')} comboboxProps={{ width: 'max-content', position: 'bottom-start', styles: { dropdown: { maxWidth: 'calc(100vw - 16px)' } } }} />
      {topics.scopes.length > 0 && (
        <Indicator disabled={!narrowed && !elsewhere} processing={elsewhere} size={7} offset={5}>
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
                  leftSection={x.value === topics.scope ? <IconCheck size={14} /> : <span style={{ width: 14 }} />}
                  rightSection={topics.working.has(x.value) ? pulse : undefined}>{x.label}</Menu.Item>
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
