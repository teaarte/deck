import { useEffect, useMemo, useRef, useState } from 'react';
import { MessageProcessor, type A2uiClientAction, type SurfaceModel } from '@a2ui/web_core/v0_9';
import type { ReactComponentImplementation } from '@a2ui/react/v0_9';
import { call } from '../lib/api';
import { events, type LiveEvent } from '../lib/events';
import i18n from '../lib/i18n';
import { notifyError } from '../lib/notify';
import catalogSchema from '../lib/catalogSchema';
import type { Action } from '../lib/catalog.gen';
import { screenKey, serverParams, type Route } from '../router';
import { act } from './act';
import { catalog } from './catalog';
import { Feed, type Message } from './feed';
import type { ScreenActions } from './ScreenActions';

const TOPICS: string[] = catalogSchema.topics;

type Processor = MessageProcessor<ReactComponentImplementation>;

// useScreen reads a screen and keeps it fresh: events read it again at most every 200 ms, merged by topic and project,
// a return to the page reads it whole; a screen already drawn updates in place, keeping the tab and what was typed.
export function useScreen(route: Route, onProjects: () => void) {
  const [surface, setSurface] = useState<SurfaceModel<ReactComponentImplementation>>();
  const [error, setError] = useState('');
  const proc = useRef<Processor>(undefined);
  const filterRef = useRef<ScreenActions['filter']>(() => undefined);
  const key = screenKey(route);

  useEffect(() => {
    let gone = false;
    const picked: Record<string, Record<string, string>> = {};
    const params = serverParams(route);
    const base = { project: route.project, screen: route.screen, record: route.record, lang: i18n.language, ...(params ? { params: JSON.stringify(params) } : {}) };
    const input = () => (Object.keys(picked).length ? { ...base, filter: JSON.stringify(picked) } : base);
    const feed = new Feed((m) => {
      try {
        proc.current!.processMessages([m as any]);
      } catch (e) {
        setError((e as Error).message);
      }
    });
    let generation = 0;
    const load = async () => {
      const mine = ++generation;
      const ticket = feed.ticket();
      try {
        const msgs = await call<Message[]>('deck.screen', input());
        if (gone || mine !== generation) return;
        if (!proc.current) {
          const p: Processor = new MessageProcessor<ReactComponentImplementation>([catalog], (a: A2uiClientAction) => {
            if (a.name === 'filter') {
              const c = a.context as { block: string; field: string; value: string };
              filter(c.block, c.field, c.value);
              return;
            }
            return act(a.context as Action).then(() => undefined);
          });
          p.onSurfaceCreated((s) => setSurface(s));
          proc.current = p;
          feed.reset();
        }
        setError('');
        feed.whole(msgs, ticket);
      } catch (e) {
        if (!gone) setError((e as Error).message);
      }
    };
    let filtering = 0;
    const filter = async (block: string, field: string, value: string) => {
      const f = { ...picked[block], [field]: value };
      if (!value) delete f[field];
      if (Object.keys(f).length) picked[block] = f;
      else delete picked[block];
      const mine = generation;
      const n = ++filtering;
      const ticket = feed.ticket();
      try {
        const msgs = await call<Message[]>('deck.screen', input());
        if (!gone && mine === generation && n === filtering) feed.feed(msgs.filter((m) => m.updateDataModel), ticket);
      } catch (e) {
        if (!gone) notifyError(e);
      }
    };
    filterRef.current = (block, field, value) => void filter(block, field, value);
    const pending = new Map<string, LiveEvent>();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const reread = async (e: LiveEvent) => {
      if (!proc.current) return;
      const mine = generation;
      const ticket = feed.ticket();
      try {
        const msgs = await call<Message[]>('deck.screen', { ...input(), topic: e.topic, ...(e.project ? { changed: e.project } : {}) });
        if (!gone && mine === generation) feed.feed(msgs, ticket);
      } catch (e) {
        if (!gone) notifyError(e);
      }
    };
    const onEvent = (e: LiveEvent) => {
      if (e.topic === 'projects') onProjects();
      if (!TOPICS.includes(e.topic)) return;
      pending.set(e.topic + ' ' + (e.project ?? ''), e);
      if (timer) return;
      timer = setTimeout(() => {
        timer = undefined;
        const es = [...pending.values()];
        pending.clear();
        es.forEach((x) => void reread(x));
      }, 200);
    };
    const stop = events(onEvent, () => void load());
    proc.current = undefined;
    setSurface(undefined);
    void load();
    return () => {
      gone = true;
      clearTimeout(timer);
      stop();
    };
  }, [key]);

  const actions = useMemo<ScreenActions>(() => ({ run: act, filter: (block, field, value) => filterRef.current(block, field, value) }), []);
  return { surface, error, key, actions };
}
