import { useState } from 'react';
import { ActionIcon, Button, Group, Menu, Select, Stack, Text, Tooltip } from '@mantine/core';
import { IconActivity, IconCheck, IconCpu, IconMessage, IconPlus, IconStack2 } from '@tabler/icons-react';
import { useTranslation } from 'react-i18next';
import type { ChatData } from './chatData';
import { notifyError } from '../lib/notify';
import { ActionsMenu } from '../ui/actions/ActionsMenu';
import { Logo } from '../ui/logo/Logo';
import { NamedIcon } from '../ui/NamedIcon';
import { press, useBlockContext } from '../screen/press';
import { MORE, type useTopics } from './useTopics';
import type { Topic } from '../lib/catalog.gen';
import { formatted, relative, when } from '../lib/format';
import s from './chat.module.css';

const check = (on: boolean) => (on ? <IconCheck size={14} /> : <span style={{ width: 14 }} />);

// TopicLine is a topic in the history: its module's icon and title, and below how long ago, the mark of the model of
// its last reply and the tokens it spent.
function TopicLine({ topic, label, checked, icon }: { topic?: Topic; label: string; checked: boolean; icon?: string }) {
  const { t } = useTranslation();
  const at = topic ? when(topic.updated_at) : undefined;
  return (
    <Group gap={10} wrap="nowrap" w="100%" data-topic-line={topic?.id}>
      <span className={s.topicIcon}>{icon ? <NamedIcon name={icon} fallback={topic?.project ?? ''} size={16} /> : <IconMessage size={16} />}</span>
      <Stack gap={0} miw={0} className="deck-grow">
        <Text size="sm" fw={checked ? 650 : 500} truncate data-topic-title>{label}</Text>
        {topic && (
          <Group gap={6} wrap="nowrap" c="dimmed" fz="xs">
            {at && <span>{relative(at, Date.now())}</span>}
            {topic.logo && <><span>·</span><span title={topic.replied_on ?? topic.model} aria-label={topic.replied_on ?? topic.model} role="img" data-topic-model-mark><Logo names={topic.logo} size={12} /></span></>}
            {(topic.tokens ?? 0) > 0 && <><span>·</span><span data-topic-tokens={topic.tokens}>{t("{{n}} tokens", { count: (topic.tokens ?? 0) >= 1000 ? 5 : topic.tokens, n: formatted(topic.tokens, { format: "compact" }) })}</span></>}
          </Group>
        )}
      </Stack>
      {checked && <IconCheck size={14} />}
    </Group>
  );
}

// TopicBar picks the topic of the chat and the model it answers on, narrows the topics to a module, starts a new one,
// and holds its buttons; it shrinks to the width it has. A pulsing dot marks the modules whose own work goes on.
export function TopicBar({ value, topics, model, setModel }: {
  value: ChatData; topics: ReturnType<typeof useTopics>; model: string; setModel: (m: string) => void;
}) {
  const { t } = useTranslation();
  const [opened, setOpened] = useState(false);
  const topic = value.topic;
  const narrowed = topics.scopes.find((x) => x.value === topics.scope && topics.scope !== topics.scopes[0]?.value);
  const own = topic ? topic.model ?? '' : model;
  const shown = own || value.model || '';
  const ctx = useBlockContext();
  // note is a model in short, such as its price and speed, beside its name; mark the mark of its provider.
  const note = (m?: string) => (m && value.notes?.[m] ? <Text size="xs" c="dimmed" data-model-note>{value.notes[m]}</Text> : undefined);
  const mark = (m?: string) => (m ? <Logo names={value.logos?.[m] ?? m} size={14} /> : undefined);
  const choose = (m: string) => {
    if (!topic) return setModel(m);
    if (value.choose) void press(ctx, value.choose, { model: m }).then((r) => { if (r.error) notifyError(new Error(r.error), t('The model was not changed')); });
  };
  return (
    <Group gap={4} wrap="nowrap" miw={0}>
      <Select size="xs" className="deck-fill" placeholder={t('New topic')} data={topics.options} value={topic ? String(topic.id) : null}
        onChange={(v) => { topics.pick(v ?? 'new'); if (v === MORE) setTimeout(() => setOpened(true)); }} data-topics
        dropdownOpened={opened} onDropdownOpen={() => setOpened(true)} onDropdownClose={() => setOpened(false)}
        leftSection={topic?.project ? <NamedIcon name={topics.iconOf(topic.project)} fallback={topic.project} size={14} /> : undefined}
        renderOption={({ option, checked }) => option.value === MORE
          ? <Text size="sm" c="var(--mantine-primary-color-filled)" fw={600} data-topics-more>{option.label}</Text>
          : <TopicLine topic={topics.topicOf(option.value)} label={option.label} checked={!!checked} icon={topics.iconOf(topics.topicOf(option.value)?.project)} />}
        searchable nothingFoundMessage={t('No topics')} maxDropdownHeight={420}
        comboboxProps={{ width: 340, position: 'bottom-start', styles: { dropdown: { maxWidth: 'calc(100vw - 16px)' } } }} />
      {(value.models?.length ?? 0) > 0 && (
        <Menu position="bottom-end" shadow="md" withinPortal>
          <Tooltip label={t('Model of this topic')}>
            <Menu.Target>
              <Button size="compact-xs" variant="light" maw={150}
                leftSection={shown ? mark(shown) : <IconCpu size={13} />} aria-label={`${t('Model of this topic')}: ${shown}`} data-topic-model={shown} data-own={own ? true : undefined}>
                <span style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>{shown}</span>
              </Button>
            </Menu.Target>
          </Tooltip>
          <Menu.Dropdown data-topic-models>
            <Menu.Label>{t('Model of this topic')}</Menu.Label>
            <Menu.Item leftSection={<Group gap={6} wrap="nowrap">{check(!own)}{mark(value.model)}</Group>} onClick={() => choose('')} data-model="" rightSection={note(value.model)}>{t("The master's own ({{model}})", { model: value.model ?? '' })}</Menu.Item>
            {value.models!.filter((m) => m !== value.model || own === m).map((m) => (
              <Menu.Item key={m} leftSection={<Group gap={6} wrap="nowrap">{check(own === m)}{mark(m)}</Group>} onClick={() => choose(m)} data-model={m} rightSection={note(m)}>{m}</Menu.Item>
            ))}
          </Menu.Dropdown>
        </Menu>
      )}
      {topics.scopes.length > 0 && (
        <Menu position="bottom-end" shadow="md" withinPortal>
          <Tooltip label={narrowed ? `${t('Module')}: ${narrowed.label}` : t('Module')}>
            <Menu.Target>
              <ActionIcon variant={narrowed ? 'light' : 'subtle'} color={narrowed ? undefined : 'gray'} size={28}
                aria-label={t('Module')} data-topic-project={topics.scope}>
                <IconStack2 size={15} />
              </ActionIcon>
            </Menu.Target>
          </Tooltip>
          <Menu.Dropdown data-topic-projects>
            {topics.scopes.map((x) => (
              <Menu.Item key={x.value} onClick={() => topics.setScope(x.value)} data-scope={x.value} leftSection={<Group gap={6} wrap="nowrap">{check(x.value === topics.scope)}{x.icon && <NamedIcon name={x.icon} fallback={x.label} size={15} />}</Group>}
                rightSection={topics.working.has(x.value) ? <IconActivity size={14} color="var(--mantine-color-green-text)" aria-label={t('The master is working')} data-busy /> : undefined}>{x.label}</Menu.Item>
            ))}
          </Menu.Dropdown>
        </Menu>
      )}
      <Tooltip label={t('New topic')}>
        <ActionIcon variant="subtle" color="gray" size={28} onClick={() => topics.pick('new')} aria-label={t('New topic')} data-new-topic><IconPlus size={16} /></ActionIcon>
      </Tooltip>
      {topic && <ActionsMenu actions={[value.archive!, value.delete!].filter(Boolean)} />}
    </Group>
  );
}
