import { flushSync } from 'react-dom';
import { createStore, useStore } from '../lib/store';

// Route is a screen and its parameters: #/p/<project>/<screen>[/<record>][?name=value&…], deck's own without p/.
export interface Route {
  project?: string;
  screen: string;
  record?: number;
  params?: Record<string, string>;
}

// LOCAL are the parameters only the client reads: the tab picked, the tab picked inside it, the dialog open, a new
// topic of the chat asked for, the size of a card a widget fills and the tiles opened to be placed.
const LOCAL = ['tab', 'sub', 'dialog', 'chat', 'widget', 'edit'];

export function parse(hash: string): Route {
  const [path, query = ''] = hash.replace(/^#\/?/, '').split('?');
  const params = Object.fromEntries(new URLSearchParams(query));
  const [kind, a, b, c] = path.split('/').map(decodeURIComponent);
  const p = Object.keys(params).length ? { params } : {};
  if (kind === 'p' && a && b) return { project: a, screen: b, record: c ? Number(c) : undefined, ...p };
  if (kind === 'deck' && a) return { screen: a, record: b ? Number(b) : undefined, ...p };
  return { screen: 'apps', ...p };
}

export function href(r: Route): string {
  const path = '#/' + (r.project ? 'p/' + encodeURIComponent(r.project) : 'deck') + '/' + encodeURIComponent(r.screen) + (r.record ? '/' + r.record : '');
  const query = new URLSearchParams(Object.entries(r.params ?? {}).filter(([, v]) => v !== '')).toString();
  return query ? path + '?' + query : path;
}

// serverParams are the parameters the screen is drawn with; those only the client reads stay out.
export function serverParams(r: Route): Record<string, string> | undefined {
  const out = Object.fromEntries(Object.entries(r.params ?? {}).filter(([k]) => !LOCAL.includes(k)));
  return Object.keys(out).length ? out : undefined;
}

// screenKey names the screen as the server draws it: a change of a tab or a dialog does not draw it again, a card of
// another size does.
export const screenKey = (r: Route) => href({ ...r, params: { ...serverParams(r), ...(r.params?.widget ? { widget: r.params.widget } : {}) } });

// picks are the params the filters of the side panel of a screen set, by the screen; a pick reads it again in place.
const picks = new Map<string, Set<string>>();
const screenOf = (r: Route) => href({ project: r.project, screen: r.screen, record: r.record });
export const pickParams = (r: Route, params: string[]) => picks.set(screenOf(r), new Set(params));

// held are the params a button gave the dialog it opened on its own screen, by the screen: only the dialog reads them,
// so the screen stays drawn as they come and go with it.
const held = new Map<string, Set<string>>();
const without = (r: Route, names?: Set<string>): Route =>
  names ? { ...r, params: Object.fromEntries(Object.entries(r.params ?? {}).filter(([k]) => !names.has(k))) } : r;

// placeKey is the key of a screen but for the picks of its side panel: the screen stays drawn while they change.
export const placeKey = (r: Route) => screenKey(without(without(r, picks.get(screenOf(r))), held.get(screenOf(r))));

// wholeKey is the key of a screen as it is read whole: the params of its open dialog read only the dialog.
export const wholeKey = (r: Route) => screenKey(without(r, held.get(screenOf(r))));

// The trail holds the addresses visited in this tab, so the back button shows only where there is a way back.
let trail: string[] = [location.hash || '#/'];
const route = createStore(parse(location.hash));

// moving is the module whose card grows into its screen, or its screen back into the card, while the move shows.
const moving = createStore<string | undefined>(undefined);
export const useMoving = () => useStore(moving);

type Transitions = Document & { startViewTransition?: (f: () => void) => unknown };
const tiles = (r: Route) => !r.project && r.screen === 'apps';

// show draws a new address; between the tiles and a module the card and the screen pass into each other, where the
// browser can and the owner keeps the motion.
function show(r: Route) {
  const was = route.get();
  const card = tiles(was) && r.project && !r.record ? r.project : tiles(r) && was.project ? was.project : undefined;
  const doc = document as Transitions;
  if (!card || !doc.startViewTransition || document.documentElement.dataset.deckMotion === 'off' || matchMedia('(prefers-reduced-motion: reduce)').matches) {
    route.set(r);
    return;
  }
  flushSync(() => moving.set(card));
  doc.startViewTransition(() => flushSync(() => route.set(r)));
}

window.addEventListener('hashchange', () => {
  const now = location.hash || '#/';
  if (trail.length > 1 && trail[trail.length - 2] === now) trail = trail.slice(0, -1);
  else if (trail[trail.length - 1] !== now) trail = [...trail, now];
  const r = parse(now);
  if (!r.params?.dialog) held.delete(screenOf(r));
  show(r);
});

export const useRoute = () => useStore(route);
export const useCanGoBack = () => useStore(route, () => trail.length) > 1;

// movers hear the owner pick a screen by a button or a link, the screen shown too, so what covers it steps aside.
const movers = new Set<() => void>();
export const onMove = (f: () => void) => (movers.add(f), () => { movers.delete(f); });
const moved = () => movers.forEach((f) => f());
document.addEventListener('click', (e) => { if ((e.target as Element | null)?.closest?.('a[href^="#/"]')) moved(); });

export function go(r: Route) {
  const now = route.get();
  if (r.params?.dialog && screenOf(r) === screenOf(now)) {
    const mine = Object.keys(r.params).filter((k) => !LOCAL.includes(k) && r.params![k] !== now.params?.[k]);
    if (mine.length) held.set(screenOf(r), new Set(mine));
  }
  location.hash = href(r);
  moved();
}

export function back() {
  history.back();
}

const withParam = (name: string, value: string | null): Route => {
  const r = route.get();
  const params = { ...(r.params ?? {}) };
  if (value === null || value === '') delete params[name];
  else params[name] = value;
  return { ...r, params: Object.keys(params).length ? params : undefined };
};

// setParam changes one parameter of the current address in a new step of the history; null removes it.
export function setParam(name: string, value: string | null) {
  location.hash = href(withParam(name, value));
}

// dropParam removes a parameter: back in the history when the step before is the address without it. A dialog closed
// takes the params it was opened with along.
export function dropParam(name: string) {
  let r = withParam(name, null);
  if (name === 'dialog') {
    r = without(r, held.get(screenOf(r)));
    held.delete(screenOf(r));
  }
  const to = href(r);
  if (trail.length > 1 && trail[trail.length - 2] === to) {
    history.back();
    return;
  }
  trail = [...trail.slice(0, -1), to];
  location.replace(to);
}
