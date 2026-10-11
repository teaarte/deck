import { useEffect, useMemo, useRef, useState } from 'react';
import { MessageProcessor, type A2uiClientAction, type SurfaceModel } from '@a2ui/web_core/v0_9';
import type { ReactComponentImplementation } from '@a2ui/react/v0_9';
import { call } from '../lib/api';
import { events, type LiveEvent } from '../lib/events';
import i18n from '../lib/i18n';
import { notifyError } from '../lib/notify';
import catalogSchema from '../lib/catalogSchema';
import type { Action } from '../lib/catalog.gen';
import { screenKey, serverParams, type Route } from '../app/router';
import { act } from './act';
import { catalog } from './catalog';
import { Feed, type Message } from './feed';
import type { ScreenActions } from './ScreenActions';

const TOPICS: string[] = catalogSchema.topics;

// rootTopics are the topics the root of a whole screen names, after which it is read again.
const rootTopics = (msgs: Message[]): string[] | undefined =>
  msgs.flatMap((m) => (m.updateComponents?.components ?? []) as Message[]).find((c) => c.id === 'root')?.topics;

type Processor = MessageProcessor<ReactComponentImplementation>;

// useScreen reads a screen and keeps it fresh: events of the topics it names read it again at most every 200 ms,
// merged by topic and project, a return to the page reads it whole; a drawn screen updates in place, keeping the tab and what was typed, or a read error.
export function useScreen(route: Route) {
  const [drawn, setDrawn] = useState<{ surface: SurfaceModel<ReactComponentImplementation>; key: string }>();
  const [error, setError] = useState('');
  const proc = useRef<Processor>(undefined);
  const filterRef = useRef<ScreenActions['filter']>(() => undefined);
  const routeRef = useRef(route);
  routeRef.current = route;
  const key = screenKey(route);

  useEffect(() => {
    let gone = false;
    const picked: Record<string, Record<string, string>> = {};
    const input = () => {
      const params = serverParams(routeRef.current);
      const base = { project: route.project, screen: route.screen, record: route.record, lang: i18n.language, ...(params ? { params: JSON.stringify(params) } : {}) };
      return Object.keys(picked).length ? { ...base, filter: JSON.stringify(picked) } : base;
    };
    const feed = new Feed((m) => {
      try {
        proc.current!.processMessages([m as any]);
      } catch (e) {
        setError((e as Error).message);
      }
    });
    let generation = 0;
    // topics are those the screen named once read; until then any topic of the catalog.
    let topics: string[] | undefined;
    // shown is set once the screen is drawn; a failed read then leaves it drawn.
    let shown = false;
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
            return act(a.context as Action).then(() => undefined, (e) => notifyError(e));
          });
          p.onSurfaceCreated((s) => {
            shown = true;
            setDrawn({ surface: s, key });
          });
          proc.current = p;
          feed.reset();
        }
        setError('');
        topics = rootTopics(msgs) ?? topics;
        feed.whole(msgs, ticket);
        // A change heard while the screen was first read is read again now that it is drawn.
        missed.splice(0).forEach((x) => void reread(x));
      } catch (e) {
        if (gone) return;
        if (shown) notifyError(e);
        else setError((e as Error).message);
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
    const missed: LiveEvent[] = [];
    const pending = new Map<string, LiveEvent>();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const reread = async (e: LiveEvent) => {
      if (!proc.current) {
        missed.push(e);
        return;
      }
      const mine = generation;
      const ticket = feed.ticket();
      try {
        const msgs = await call<Message[]>('deck.screen', { ...input(), topic: e.topic, ...(e.project ? { changed: e.project } : {}) });
        if (gone || mine !== generation) return;
        topics = rootTopics(msgs) ?? topics;
        feed.feed(msgs, ticket);
      } catch (e) {
        if (!gone) notifyError(e);
      }
    };
    const onEvent = (e: LiveEvent) => {
      if (!(topics ?? TOPICS).includes(e.topic)) return;
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
    setDrawn(undefined);
    void load();
    return () => {
      gone = true;
      clearTimeout(timer);
      stop();
    };
  }, [key]);

  const actions = useMemo<ScreenActions>(() => ({ run: act, filter: (block, field, value) => filterRef.current(block, field, value) }), []);
  // A surface of the screen before is not drawn again under the key of the next one.
  const surface = drawn?.key === key ? drawn.surface : undefined;
  return { surface, error, key, actions };
}
