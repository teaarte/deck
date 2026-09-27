import { useEffect, useRef, useState } from 'react';
import { MessageProcessor, type A2uiClientAction, type SurfaceModel } from '@a2ui/web_core/v0_9';
import { A2uiSurface, type ReactComponentImplementation } from '@a2ui/react/v0_9';
import { Alert, Loader } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { useTranslation } from 'react-i18next';
import { call, events } from './api';
import { catalog, known, setStale, UNKNOWN, type Action } from './catalog';
import i18n from './i18n';

export const VERSION = 'v0.9';
const TOPICS = ['collection', 'asks', 'processes', 'unattended', 'health', 'log'];

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

async function act(a: A2uiClientAction) {
  const c = a.context as Action;
  if (a.name === 'open') {
    location.hash = href({ project: c.project || undefined, screen: c.screen!, record: c.record });
    return;
  }
  try {
    const out = await call<Record<string, any>>(c.command!, c.params ?? {});
    const text = out && typeof out === 'object' && 'process' in out ? i18n.t('started', { id: out.process }) : i18n.t('done');
    notifications.show({ message: text, color: 'green' });
  } catch (e) {
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
    const input = { project: route.project, screen: route.screen, record: route.record };
    const feed = (msgs: Message[]) => {
      for (const m of prepare(msgs)) {
        try {
          proc.current!.processMessages([m as any]);
        } catch (e) {
          setError((e as Error).message);
        }
      }
    };
    const load = async () => {
      try {
        const msgs = await call<Message[]>('deck.screen', input);
        if (gone) return;
        const p = new MessageProcessor<ReactComponentImplementation>([catalog], act);
        p.onSurfaceCreated((s) => setSurface(s));
        proc.current = p;
        setError('');
        feed(msgs);
        setStale(false);
      } catch (e) {
        if (!gone) setError((e as Error).message);
      }
    };
    const pending = new Set<string>();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const reread = async (topic: string) => {
      if (!proc.current) return;
      try {
        const msgs = await call<Message[]>('deck.screen', { ...input, topic });
        if (!gone) feed(msgs);
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
