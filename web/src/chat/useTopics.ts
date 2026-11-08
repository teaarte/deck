import { useEffect, useRef, useState } from 'react';
import { useLocalStorage } from '@mantine/hooks';
import type { ComponentContext } from '@a2ui/web_core/v0_9';
import { useTranslation } from 'react-i18next';
import type { ChatData } from './chatData';
import type { Topic } from '../lib/catalog.gen';
import { useRoute } from '../app/router';
import { useMenuData } from '../hooks/useMenu';
import { readBlock } from '../screen/press';
import { onTopicOpen } from './chatStore';

const ALL = '*';
const NONE = '-';
// MORE is the option that shows every topic of the scope; LAST how many the history shows before it.
export const MORE = '+';
const LAST = 10;

// useTopics picks the topic the chat shows and lists the history: the open topic, then the last of the module of the
// screen or the one picked, all after Show all; the modules opened last come first.
export function useTopics(value: ChatData, context: ComponentContext) {
  const { t } = useTranslation();
  const menu = useMenuData();
  const route = useRoute();
  const pinned = useRef(false);
  // scope is the module the owner picked; null follows the module of the screen.
  const [scope, setScope] = useState<string | null>(null);
  const [all, setAll] = useState(false);
  const [recent, setRecent] = useLocalStorage<string[]>({ key: 'deck-chat-modules', defaultValue: [] });
  const pick = (topic: string) => {
    if (topic === MORE) return setAll(true);
    pinned.current = true;
    readBlock(context, 'filter', { field: 'topic', value: topic });
  };
  const topic = value.topic;
  useEffect(() => onTopicOpen((id) => pick(String(id))), []);
  useEffect(() => { if (topic && !pinned.current) pick(String(topic.id)); }, [topic?.id]);
  const projects = [...new Set([...value.topics, ...value.archived].map((x) => x.project).filter(Boolean) as string[])].sort();
  const wanted = scope ?? route.project ?? ALL;
  const here = wanted === ALL || wanted === NONE || projects.includes(wanted) ? wanted : ALL;
  useEffect(() => setAll(false), [here]);
  const inScope = (x: Topic) => here === ALL || (here === NONE ? !x.project : x.project === here);
  // The topic open comes first, whatever its module, then the last of the scope.
  const open = value.topics.find((x) => x.id === topic?.id) ?? value.archived.find((x) => x.id === topic?.id) ?? topic;
  const scoped = value.topics.filter((x) => x.id !== topic?.id && inScope(x));
  const archivedScoped = value.archived.filter((x) => x.id !== topic?.id && inScope(x));
  const total = scoped.length + archivedScoped.length + (open && inScope(open) ? 1 : 0);
  const entry = (p?: string) => menu.projects.find((m) => m.project === p);
  const option = (x: Topic) => ({ value: String(x.id), label: x.title });
  const room = open && inScope(open) ? LAST - 1 : LAST;
  const shown = [...(open ? [open] : []), ...(all ? scoped : scoped.slice(0, room))];
  const archived = all ? archivedScoped.map(option) : [];
  const more = !all && (scoped.length > room || archivedScoped.length > 0) ? [{ value: MORE, label: t('Show all ({{n}})', { n: total }) }] : [];
  const options = [...shown.map(option), ...more, ...(archived.length ? [{ group: t('Archived'), items: archived }] : [])];
  const byId = new Map([...(topic ? [topic] : []), ...value.topics, ...value.archived].map((x) => [String(x.id), x]));
  // A module works when processes of its own run, not when the master talks about it.
  const working = new Set<string>();
  menu.projects.forEach((m) => { if (m.project && m.running && projects.includes(m.project)) working.add(m.project); });
  const rank = (v: string) => (recent.includes(v) ? recent.indexOf(v) : recent.length);
  const modules = [{ value: NONE, label: t('Without a module'), icon: undefined as string | undefined },
    ...projects.map((p) => ({ value: p, label: p, icon: entry(p)?.icon ?? 'box' }))]
    .sort((a, b) => rank(a.value) - rank(b.value));
  const scopes = [{ value: ALL, label: t('All modules'), icon: undefined as string | undefined }, ...modules];
  const narrow = (v: string) => {
    setScope(v);
    if (v === ALL) return;
    setRecent([v, ...recent.filter((x) => x !== v)].slice(0, 20));
    const last = [...value.topics].filter((x) => (v === NONE ? !x.project : x.project === v))
      .sort((a, b) => b.updated_at.localeCompare(a.updated_at))[0];
    pick(last ? String(last.id) : 'new');
  };
  return {
    pick,
    options,
    topicOf: (id: string) => byId.get(id),
    iconOf: (project?: string) => (project ? entry(project)?.icon ?? 'box' : undefined),
    working,
    scopes: projects.length ? scopes : [],
    scope: here,
    setScope: (v: string | null) => narrow(v ?? ALL),
  };
}
