import { useEffect, useRef, useState } from 'react';
import { MessageProcessor, type A2uiClientAction, type SurfaceModel } from '@a2ui/web_core/v0_9';
import type { ReactComponentImplementation } from '@a2ui/react/v0_9';
import { call } from '../lib/api';
import { events, type LiveEvent } from '../lib/events';
import i18n from '../lib/i18n';
import { notifyError } from '../lib/notify';
import catalogSchema from '../lib/catalogSchema';
import type { Action } from '../lib/catalog.gen';
import { screenKey, serverParams, type Route } from '../app/router';
import { catalog } from './catalog';
import { Feed, type Message } from './feed';
import { PRESS, run } from './press';

const TOPICS: string[] = catalogSchema.topics;

// rootTopics are the topics the root of a whole screen names, after which it is read again.
const rootTopics = (msgs: Message[]): string[] | undefined =>
  msgs.flatMap((m) => (m.updateComponents?.components ?? []) as Message[]).find((c) => c.id === 'root')?.topics;

type Processor = MessageProcessor<ReactComponentImplementation>;

// useScreen reads a screen and keeps it fresh by the topics it names; its one handler of actions replies to a press in
// the data of the surface and reads the block of a filter, a next page or an opened dialog.
export function useScreen(route: Route) {
  const [drawn, setDrawn] = useState<{ surface: SurfaceModel<ReactComponentImplementation>; key: string }>();
  const [error, setError] = useState('');
  const proc = useRef<Processor>(undefined);
  const routeRef = useRef(route);
  routeRef.current = route;
  const key = screenKey(route);

  useEffect(() => {
    let gone = false;
    const picked: Record<string, Record<string, string>> = {};
    const pages: Record<string, number> = {};
    const input = (extra: Record<string, unknown> = {}) => {
      const r = routeRef.current;
      const params = serverParams(r);
      return {
        project: route.project, screen: route.screen, record: route.record, lang: i18n.language,
        ...(params ? { params: JSON.stringify(params) } : {}),
        ...(r.params?.dialog ? { dialog: r.params.dialog } : {}),
        ...(Object.keys(picked).length ? { filter: JSON.stringify(picked) } : {}),
        ...(Object.keys(pages).length ? { pages: JSON.stringify(pages) } : {}),
        ...extra,
      };
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
    const handle = async (a: A2uiClientAction) => {
      const c = (a.context ?? {}) as Record<string, unknown>;
      const block = String(c.block ?? '');
      switch (a.name) {
        case 'filter': {
          const f = { ...picked[block], [String(c.field)]: String(c.value ?? '') };
          if (!c.value) delete f[String(c.field)];
          if (Object.keys(f).length) picked[block] = f;
          else delete picked[block];
          return readBlock(block);
        }
        case 'more':
          pages[block] = (pages[block] ?? 1) + 1;
          return readBlock(block);
        case 'dialog':
          return readBlock(block, { dialog: String(c.dialog ?? '') });
      }
      const { reply, ...pressed } = c as Action & { reply?: string };
      const r = await run(pressed);
      if (reply) proc.current?.model.getSurface(a.surfaceId)?.dataModel.set(PRESS + reply, r);
    };
    const load = async () => {
      const mine = ++generation;
      const ticket = feed.ticket();
      try {
        const msgs = await call<Message[]>('deck.screen', input());
        if (gone || mine !== generation) return;
        if (!proc.current) {
          const p: Processor = new MessageProcessor<ReactComponentImplementation>([catalog], handle);
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
    // readBlock reads the data of one block and those inside it; a read of a block with a when draws the screen whole.
    const readBlock = async (block: string, extra: Record<string, unknown> = {}) => {
      const mine = generation;
      const ticket = feed.ticket();
      try {
        const msgs = await call<Message[]>('deck.screen', input({ block, ...extra }));
        if (gone || mine !== generation) return;
        topics = rootTopics(msgs) ?? topics;
        feed.feed(msgs, ticket);
      } catch (e) {
        if (!gone) notifyError(e);
      }
    };
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
        const msgs = await call<Message[]>('deck.screen', input({ topic: e.topic, ...(e.project ? { changed: e.project } : {}) }));
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

  // A surface of the screen before is not drawn again under the key of the next one.
  const surface = drawn?.key === key ? drawn.surface : undefined;
  return { surface, error, key };
}
