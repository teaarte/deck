import { useEffect, useRef, useState } from 'react';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { useTranslation } from 'react-i18next';
import type { ChatValue } from '../lib/catalog.gen';
import { onTopicOpen } from './chatStore';

const ALL = '*';
const NONE = '-';

// useTopics picks the topic the chat shows: the one the owner or another screen picked, else the one the server
// offers; the topics on offer narrow to a module.
export function useTopics(value: ChatValue, context: ComponentContext) {
  const { t } = useTranslation();
  const pinned = useRef(false);
  const [scope, setScope] = useState<string | null>(null);
  const pick = (topic: string) => {
    pinned.current = true;
    void context.dispatchAction({ event: { name: 'filter', context: { block: context.componentModel.id, field: 'topic', value: topic } } });
  };
  const topic = value.topic;
  useEffect(() => onTopicOpen((id) => pick(String(id))), []);
  useEffect(() => { if (topic && !pinned.current) pick(String(topic.id)); }, [topic?.id]);
  const projects = [...new Set([...value.topics, ...value.archived].map((x) => (x as { project?: string }).project).filter(Boolean) as string[])].sort();
  const inProject = (x: { project?: string }) => !scope || (scope === NONE ? !x.project : x.project === scope);
  const options = [
    ...value.topics.filter(inProject).map((x) => ({ value: String(x.id), label: x.title })),
    ...value.archived.filter(inProject).map((x) => ({ value: String(x.id), label: t('chat.archived', { title: x.title }) })),
  ];
  const scopes = [{ value: ALL, label: t('chat.all_modules') }, { value: NONE, label: t('chat.no_module') }, ...projects.map((p) => ({ value: p, label: p }))];
  return {
    pick,
    options,
    scopes: projects.length ? scopes : [],
    scope: scope ?? ALL,
    setScope: (v: string | null) => setScope(v === ALL ? null : v),
  };
}
