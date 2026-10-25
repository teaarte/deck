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

// The trail holds the addresses visited in this tab, so the back button shows only where there is a way back.
let trail: string[] = [location.hash || '#/'];
const route = createStore(parse(location.hash));
window.addEventListener('hashchange', () => {
  const now = location.hash || '#/';
  if (trail.length > 1 && trail[trail.length - 2] === now) trail = trail.slice(0, -1);
  else if (trail[trail.length - 1] !== now) trail = [...trail, now];
  route.set(parse(now));
});

export const useRoute = () => useStore(route);
export const useCanGoBack = () => useStore(route, () => trail.length) > 1;

// movers hear the owner pick a screen by a button or a link, the screen shown too, so what covers it steps aside.
const movers = new Set<() => void>();
export const onMove = (f: () => void) => (movers.add(f), () => { movers.delete(f); });
const moved = () => movers.forEach((f) => f());
document.addEventListener('click', (e) => { if ((e.target as Element | null)?.closest?.('a[href^="#/"]')) moved(); });

export function go(r: Route) {
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

// dropParam removes a parameter: back in the history when the step before is the address without it.
export function dropParam(name: string) {
  const to = href(withParam(name, null));
  if (trail.length > 1 && trail[trail.length - 2] === to) {
    history.back();
    return;
  }
  trail = [...trail.slice(0, -1), to];
  location.replace(to);
}
