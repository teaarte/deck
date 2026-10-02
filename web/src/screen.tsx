import { useEffect, useRef, useState } from 'react';
import { MessageProcessor, type A2uiClientAction, type SurfaceModel } from '@a2ui/web_core/v0_9';
import { A2uiSurface, type ReactComponentImplementation } from '@a2ui/react/v0_9';
import { Alert, Loader } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { useTranslation } from 'react-i18next';
import { call, events } from './api';
import { catalog, known, UNKNOWN, type Action } from './catalog';
import { outcomes } from './actions';
import { openTopic } from './chat';
import { setStale } from './block';
import i18n from './i18n';
import catalogSchema from '../../schemas/catalog.json';

export const VERSION = 'v0.9';
const TOPICS: string[] = catalogSchema.topics;

export interface Route {
  project?: string;
  screen: string;
  record?: number;
}

export const href = (r: Route) =>
  '#/' + (r.project ? 'p/' + encodeURIComponent(r.project) : 'deck') + '/' + encodeURIComponent(r.screen) + (r.record ? '/' + r.record : '');

type Message = Record<string, any> & { version?: string };

// prepare checks the protocol version and swaps components the client does not know for open in web.
function prepare(msgs: Message[]): Message[] {
  for (const m of msgs) {
    if (m.version !== VERSION) throw new Error(i18n.t('screen.version', { got: m.version ?? '?', want: VERSION }));
  }
  return msgs.map((m) => {
    if (!m.updateComponents) return m;
    const components = (m.updateComponents.components as Message[]).map((c) =>
      known.has(c.component) ? c : { id: c.id, component: UNKNOWN, type: String(c.component) });
    return { ...m, updateComponents: { ...m.updateComponents, components } };
  });
}

// summary is the line a command result shows as: a started process, a text, or its JSON cut short.
function summary(out: unknown): string {
  if (out && typeof out === 'object') {
    const o = out as Record<string, unknown>;
    if (typeof o.process === 'number') return i18n.t('started', { id: o.process });
    const rest = Object.fromEntries(Object.entries(o).filter(([k]) => k !== 'ok'));
    const shown = 'result' in rest ? rest.result : Object.keys(rest).length ? rest : undefined;
    if (shown === undefined || shown === null) return i18n.t('done');
    if (typeof shown === 'string') return shown;
    const text = JSON.stringify(shown);
    return text.length > 200 ? text.slice(0, 200) + '…' : text;
  }
  return typeof out === 'string' && out ? out : i18n.t('done');
}

async function act(a: A2uiClientAction) {
  const c = a.context as Action & { ticket?: string };
  if (a.name === 'open') {
    location.hash = href({ project: c.project || undefined, screen: c.screen!, record: c.record });
    return;
  }
  try {
    const out = await call(c.command!, c.params ?? {});
    const topic = (out as { topic?: unknown } | null)?.topic;
    if (c.chat && typeof topic === 'number') {
      openTopic(topic);
      return;
    }
    notifications.show({ message: summary(out), color: 'green' });
  } catch (e) {
    if (c.ticket) outcomes.set(c.ticket, false);
    notifications.show({ title: c.label || i18n.t('verb.' + (c.verb ?? '')), message: (e as Error).message, color: 'red', autoClose: 10000 });
  }
}

export function ScreenView({ route, onProjects }: { route: Route; onProjects: () => void }) {
  const { t } = useTranslation();
  const [surface, setSurface] = useState<SurfaceModel<ReactComponentImplementation>>();
  const [error, setError] = useState('');
  const proc = useRef<MessageProcessor<ReactComponentImplementation>>(undefined);
  const key = href(route);

  useEffect(() => {
    let gone = false;
    const picked: Record<string, Record<string, string>> = {};
    const base = { project: route.project, screen: route.screen, record: route.record };
    const input = () => (Object.keys(picked).length ? { ...base, filter: JSON.stringify(picked) } : base);
    // Every read takes a ticket; the data of a block is never replaced by a read older than the one it shows.
    let tickets = 0;
    let fresh: Record<string, number> = {};
    const feed = (msgs: Message[], ticket: number) => {
      for (const m of prepare(msgs)) {
        if (m.createSurface && fresh.root !== undefined) continue;
        let parts: Message[] = [m];
        const dm = m.updateDataModel;
        if (dm && dm.path === '/' && dm.value && typeof dm.value === 'object' && fresh.root !== undefined) {
          parts = Object.entries(dm.value).map(([id, value]) => ({ ...m, updateDataModel: { ...dm, path: '/' + id, value } }));
        }
        for (const part of parts) {
          const path = part.updateDataModel?.path as string | undefined;
          const id = path && path !== '/' ? path.slice(1) : undefined;
          if (id && (fresh[id] ?? 0) > ticket) continue;
          if (id) fresh[id] = ticket;
          try {
            proc.current!.processMessages([part as any]);
          } catch (e) {
            setError((e as Error).message);
          }
        }
      }
    };
    let generation = 0;
    const load = async () => {
      const mine = ++generation;
      const ticket = ++tickets;
      try {
        const msgs = await call<Message[]>('deck.screen', input());
        if (gone || mine !== generation) return;
        // A read of a screen already drawn updates it in place, keeping the tab picked and what the owner typed.
        if (proc.current) {
          setError('');
          feed(msgs, ticket);
          fresh.root = ticket;
          setStale(false);
          return;
        }
        const p = new MessageProcessor<ReactComponentImplementation>([catalog], (a) => (a.name === 'filter' ? filter(a) : act(a)));
        p.onSurfaceCreated((s) => setSurface(s));
        proc.current = p;
        setError('');
        fresh = {};
        feed(msgs, ticket);
        fresh.root = ticket;
        setStale(false);
      } catch (e) {
        if (!gone) setError((e as Error).message);
      }
    };
    let filtering = 0;
    const filter = async (a: A2uiClientAction) => {
      const { block, field, value } = a.context as { block: string; field: string; value: string };
      const f = { ...picked[block], [field]: value };
      if (!value) delete f[field];
      if (Object.keys(f).length) picked[block] = f;
      else delete picked[block];
      const mine = generation;
      const n = ++filtering;
      const ticket = ++tickets;
      try {
        const msgs = await call<Message[]>('deck.screen', input());
        if (!gone && mine === generation && n === filtering) feed(msgs.filter((m) => m.updateDataModel), ticket);
      } catch (e) {
        if (!gone) notifications.show({ message: (e as Error).message, color: 'red' });
      }
    };
    const pending = new Set<string>();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const reread = async (topic: string) => {
      if (!proc.current) return;
      const mine = generation;
      const ticket = ++tickets;
      try {
        const msgs = await call<Message[]>('deck.screen', { ...input(), topic });
        if (!gone && mine === generation) feed(msgs, ticket);
      } catch (e) {
        if (!gone) notifications.show({ message: (e as Error).message, color: 'red' });
      }
    };
    const onTopic = (topic: string) => {
      if (topic === 'projects') {
        onProjects();
        void load();
        return;
      }
      if (!TOPICS.includes(topic)) return;
      pending.add(topic);
      clearTimeout(timer);
      timer = setTimeout(() => {
        const ts = [...pending];
        pending.clear();
        ts.forEach((x) => void reread(x));
      }, 200);
    };
    let down = false;
    const stop = events(onTopic, (live) => {
      if (live && down) void load();
      down = !live;
      setStale(!live);
    });
    const back = () => { if (!document.hidden) void load(); };
    document.addEventListener('visibilitychange', back);
    proc.current = undefined;
    setSurface(undefined);
    void load();
    return () => {
      gone = true;
      clearTimeout(timer);
      stop();
      document.removeEventListener('visibilitychange', back);
    };
  }, [key]);

  if (error) return <Alert color="red" title={t('screen.failed')} data-screen-error>{error}</Alert>;
  if (!surface) return <Loader />;
  return <A2uiSurface key={surface.id + key} surface={surface} />;
}
