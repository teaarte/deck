import { ActionIcon, Button, Group, Menu, Select, Tooltip } from '@mantine/core';
import { IconActivity, IconCheck, IconCpu, IconPlus, IconStack2 } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { ChatValue } from '../lib/catalog.gen';
import { notifyError } from '../lib/notify';
import { ActionsMenu } from '../ui/actions/ActionsMenu';
import { press, useBlockContext } from '../screen/press';
import type { useTopics } from './useTopics';

const check = (on: boolean) => (on ? <IconCheck size={14} /> : <span style={{ width: 14 }} />);

// TopicBar picks the topic of the chat and the model it answers on, narrows the topics to a module, starts a new one,
// and holds its buttons; it shrinks to the width it has. A pulsing dot marks the modules whose own work goes on.
export function TopicBar({ value, topics, model, setModel }: {
  value: ChatValue; topics: ReturnType<typeof useTopics>; model: string; setModel: (m: string) => void;
}) {
  const { t } = useTranslation();
  const topic = value.topic;
  const narrowed = topics.scopes.find((x) => x.value === topics.scope && topics.scope !== topics.scopes[0]?.value);
  const own = topic ? topic.model ?? '' : model;
  const shown = own || value.model || '';
  const ctx = useBlockContext();
  const choose = (m: string) => {
    if (!topic) return setModel(m);
    if (value.choose) void press(ctx, value.choose, { model: m }).then((r) => { if (r.error) notifyError(new Error(r.error), t('chat.model_failed')); });
  };
  return (
    <Group gap={4} wrap="nowrap" miw={0}>
      <Select size="xs" radius="xl" style={{ flex: 1, minWidth: 0 }} placeholder={t('chat.new_topic')} data={topics.options} value={topic ? String(topic.id) : null}
        onChange={(v) => topics.pick(v ?? 'new')} data-topics
        renderOption={({ option, checked }) => <Group gap={8} wrap="nowrap">{check(!!checked)}{option.label}</Group>}
        searchable nothingFoundMessage={t('chat.no_topics')} comboboxProps={{ width: 'max-content', position: 'bottom-start', styles: { dropdown: { maxWidth: 'calc(100vw - 16px)' } } }} />
      {(value.models?.length ?? 0) > 0 && (
        <Menu position="bottom-end" shadow="md" radius="md" withinPortal>
          <Tooltip label={t('chat.model')}>
            <Menu.Target>
              <Button size="compact-xs" radius="xl" variant={own ? 'light' : 'subtle'} color={own ? undefined : 'gray'} maw={130}
                leftSection={<IconCpu size={13} />} aria-label={`${t('chat.model')}: ${shown}`} data-topic-model={shown}>
                <span style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>{shown}</span>
              </Button>
            </Menu.Target>
          </Tooltip>
          <Menu.Dropdown data-topic-models>
            <Menu.Label>{t('chat.model')}</Menu.Label>
            <Menu.Item leftSection={check(!own)} onClick={() => choose('')} data-model="">{t('chat.master_model', { model: value.model ?? '' })}</Menu.Item>
            {value.models!.filter((m) => m !== value.model || own === m).map((m) => (
              <Menu.Item key={m} leftSection={check(own === m)} onClick={() => choose(m)} data-model={m}>{m}</Menu.Item>
            ))}
          </Menu.Dropdown>
        </Menu>
      )}
      {topics.scopes.length > 0 && (
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
              <Menu.Item key={x.value} onClick={() => topics.setScope(x.value)} data-scope={x.value} leftSection={check(x.value === topics.scope)}
                rightSection={topics.working.has(x.value) ? <IconActivity size={14} color="var(--mantine-color-green-7)" aria-label={t('chat.working')} data-busy /> : undefined}>{x.label}</Menu.Item>
            ))}
          </Menu.Dropdown>
        </Menu>
      )}
      <Tooltip label={t('chat.new_topic')}>
        <ActionIcon variant="subtle" color="gray" radius="xl" size={28} onClick={() => topics.pick('new')} aria-label={t('chat.new_topic')} data-new-topic><IconPlus size={16} /></ActionIcon>
      </Tooltip>
      {topic && <ActionsMenu actions={[value.archive!, value.delete!].filter(Boolean)} />}
    </Group>
  );
}
