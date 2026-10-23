import { useEffect, useRef, useState } from 'react';
import { useLocalStorage } from '@mantine/hooks';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { useTranslation } from 'react-i18next';
import type { ChatValue } from '../lib/catalog.gen';
import { useMenuData } from '../hooks/useMenu';
import { readBlock } from '../screen/press';
import { onTopicOpen } from './chatStore';

const ALL = '*';
const NONE = '-';

// useTopics picks the topic the chat shows: the one the owner or another screen picked, else the one the server
// offers; the topics on offer narrow to a module, which opens its last topic. The modules opened last come first.
export function useTopics(value: ChatValue, context: ComponentContext) {
  const { t } = useTranslation();
  const menu = useMenuData();
  const pinned = useRef(false);
  const [scope, setScope] = useState<string | null>(null);
  const [recent, setRecent] = useLocalStorage<string[]>({ key: 'deck-chat-modules', defaultValue: [] });
  const pick = (topic: string) => {
    pinned.current = true;
    readBlock(context, 'filter', { field: 'topic', value: topic });
  };
  const topic = value.topic;
  useEffect(() => onTopicOpen((id) => pick(String(id))), []);
  useEffect(() => { if (topic && !pinned.current) pick(String(topic.id)); }, [topic?.id]);
  const projects = [...new Set([...value.topics, ...value.archived].map((x) => (x as { project?: string }).project).filter(Boolean) as string[])].sort();
  const inProject = (x: { project?: string }) => !scope || (scope === NONE ? !x.project : x.project === scope);
  // The topic open comes first; the archived ones are a group of their own.
  const first = <T extends { id: number }>(xs: T[]) => {
    const i = xs.findIndex((x) => x.id === topic?.id);
    return i > 0 ? [xs[i], ...xs.slice(0, i), ...xs.slice(i + 1)] : xs;
  };
  const active = first(value.topics.filter(inProject)).map((x) => ({ value: String(x.id), label: x.title }));
  const archived = first(value.archived.filter(inProject)).map((x) => ({ value: String(x.id), label: x.title }));
  const options = archived.length ? [...active, { group: t('chat.archived'), items: archived }] : active;
  // A module works when processes of its own run, not when the master talks about it.
  const working = new Set<string>();
  menu.projects.forEach((m) => { if (m.project && m.running && projects.includes(m.project)) working.add(m.project); });
  const rank = (v: string) => (recent.includes(v) ? recent.indexOf(v) : recent.length);
  const modules = [{ value: NONE, label: t('chat.no_module') }, ...projects.map((p) => ({ value: p, label: p }))]
    .sort((a, b) => rank(a.value) - rank(b.value));
  const scopes = [{ value: ALL, label: t('chat.all_modules') }, ...modules];
  const open = (v: string) => {
    setScope(v === ALL ? null : v);
    if (v === ALL) return;
    setRecent([v, ...recent.filter((x) => x !== v)].slice(0, 20));
    const last = [...value.topics].filter((x) => (v === NONE ? !x.project : x.project === v))
      .sort((a, b) => b.updated_at.localeCompare(a.updated_at))[0];
    pick(last ? String(last.id) : 'new');
  };
  return {
    pick,
    options,
    working,
    scopes: projects.length ? scopes : [],
    scope: scope ?? ALL,
    setScope: (v: string | null) => open(v ?? ALL),
  };
}
