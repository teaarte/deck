import { useEffect, useRef, useState } from 'react';
import { MessageProcessor, type A2uiClientAction, type SurfaceModel } from '@a2ui/web_core/v0_9';
import type { ReactComponentImplementation } from '@a2ui/react/v0_9';
import { call } from '../lib/api';
import { events, type LiveEvent } from '../lib/events';
import i18n from '../lib/i18n';
import { useTranslation } from 'react-i18next';
import { notifyError } from '../lib/notify';
import catalogSchema from '../lib/catalogSchema';
import type { Action } from '../lib/catalog.gen';
import { pickParams, placeKey, serverParams, wholeKey, type Route } from '../app/router';
import { catalog } from './catalog';
import { remember, remembered } from './cache';
import { Feed, type Message } from './feed';
import { PRESS, run } from './press';

const TOPICS: string[] = catalogSchema.topics;

// rootTopics are the topics the root of a whole screen names, after which it is read again.
const rootTopics = (msgs: Message[]): string[] | undefined =>
  msgs.flatMap((m) => (m.updateComponents?.components ?? []) as Message[]).find((c) => c.id === 'root')?.topics;

type Processor = MessageProcessor<ReactComponentImplementation>;

// useScreen reads a screen and keeps it fresh by its topics, read again once shown when hidden; its one handler of
// actions replies to a press and reads the block of a filter, a next page, an opened dialog or a tab shown.
export function useScreen(route: Route, shownNow = true) {
  const [drawn, setDrawn] = useState<{ surface: SurfaceModel<ReactComponentImplementation>; key: string }>();
  const [error, setError] = useState('');
  const proc = useRef<Processor>(undefined);
  const routeRef = useRef(route);
  routeRef.current = route;
  const lang = useTranslation().i18n.language;
  const shownRef = useRef(shownNow);
  shownRef.current = shownNow;
  // missedHidden is set by a change heard while the screen was not shown; again reads it whole.
  const missedHidden = useRef(false);
  const again = useRef<() => void>(() => undefined);
  // A pick of the side panel reads the screen again into the same surface; another key draws another screen. A
  // screen whose picks became known after its first read keeps its key.
  const place = useRef<{ key: string; route: Route }>(undefined);
  const key = place.current && placeKey(place.current.route) === placeKey(route) ? place.current.key : placeKey(route);
  place.current = { key, route };
  const whole = wholeKey(route);

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
        ...(r.params?.tab ? { tab: r.params.tab } : {}),
        ...(r.params?.sub ? { sub: r.params.sub } : {}),
        ...(r.params?.widget ? { widget: r.params.widget } : {}),
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
    // wide is the ticket of the last read of the whole screen or of a topic, which leaves the tabs not shown as they were.
    let wide = 0;
    // topics are those the screen named once read; until then any topic of the catalog.
    let topics: string[] | undefined;
    // shown is set once the screen is drawn; a failed read then leaves it drawn.
    let shown = false;
    const handle = async (p: Processor, a: A2uiClientAction) => {
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
        case 'tab':
          // A tab shown with what the last wide read gave it is not read again.
          const ids = (c.children as string[] | undefined) ?? [];
          if (wide > 0 && ids.length > 0 && ids.every((id) => feed.since(id) >= wide)) return;
          return readBlock(block, { [String(c.param)]: String(c.key ?? '') });
      }
      const { reply, ...pressed } = c as Action & { reply?: string };
      const r = await run(pressed);
      // The reply goes to the surface pressed, also one still shown while the next screen loads.
      if (reply) p.model.getSurface(a.surfaceId)?.dataModel.set(PRESS + reply, r);
    };
    const kept = () => wholeKey(routeRef.current) + ' ' + i18n.language;
    // draw feeds a whole read to the processor of the screen, made by the first.
    const draw = (msgs: Message[], ticket: number) => {
      if (!proc.current) {
        const p: Processor = new MessageProcessor<ReactComponentImplementation>([catalog], (a) => handle(p, a));
        p.onSurfaceCreated((s) => {
          shown = true;
          setDrawn({ surface: s, key });
        });
        proc.current = p;
        feed.reset();
      }
      topics = rootTopics(msgs) ?? topics;
      pickParams(routeRef.current, msgs.flatMap((m) => (m.updateComponents?.components ?? []) as Message[])
        .filter((c) => c.component === 'filter').map((c) => String(c.param)));
      feed.whole(msgs, ticket);
    };
    const load = async () => {
      const mine = ++generation;
      const ticket = feed.ticket();
      try {
        const msgs = await call<Message[]>('deck.screen_get', input());
        if (gone || mine !== generation) return;
        setError('');
        wide = ticket;
        draw(msgs, ticket);
        remember(kept(), msgs, true);
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
        const msgs = await call<Message[]>('deck.screen_get', input({ block, ...extra }));
        if (gone || mine !== generation) return;
        topics = rootTopics(msgs) ?? topics;
        feed.feed(msgs, ticket);
        remember(kept(), msgs, false);
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
        const msgs = await call<Message[]>('deck.screen_get', input({ topic: e.topic, ...(e.project ? { changed: e.project } : {}) }));
        if (gone || mine !== generation) return;
        wide = ticket;
        topics = rootTopics(msgs) ?? topics;
        feed.feed(msgs, ticket);
        remember(kept(), msgs, false);
      } catch (e) {
        if (!gone) notifyError(e);
      }
    };
    const onEvent = (e: LiveEvent) => {
      if (!(topics ?? TOPICS).includes(e.topic)) return;
      if (!shownRef.current) {
        missedHidden.current = true;
        return;
      }
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
    again.current = () => void load();
    proc.current = undefined;
    setDrawn(undefined);
    // A screen read before shows at once and is read again.
    const before = remembered(kept());
    if (before) draw(before, feed.ticket());
    void load();
    return () => {
      gone = true;
      clearTimeout(timer);
      stop();
    };
  }, [key]);

  useEffect(() => {
    if (shownNow && missedHidden.current) {
      missedHidden.current = false;
      again.current();
    }
  }, [shownNow]);
  const picked = useRef({ key, whole });
  useEffect(() => {
    const was = picked.current;
    picked.current = { key, whole };
    if (was.key === key && was.whole !== whole) again.current();
  }, [key, whole]);
  // Another language reads the screen again into the same surface, so what is open and typed stays.
  const first = useRef(true);
  useEffect(() => {
    if (first.current) first.current = false;
    else if (shownRef.current) again.current();
    else missedHidden.current = true;
  }, [lang]);

  // A surface of the screen before is not drawn again under the key of the next one.
  const surface = drawn?.key === key ? drawn.surface : undefined;
  return { surface, error, key };
}
