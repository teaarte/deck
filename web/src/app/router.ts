import { useSyncExternalStore } from 'react';

// Route is a screen and its parameters: #/p/<project>/<screen>[/<record>][?name=value&…], deck's own without p/.
export interface Route {
  project?: string;
  screen: string;
  record?: number;
  params?: Record<string, string>;
}

// LOCAL are the parameters only the client reads: the tab picked and the dialog open.
const LOCAL = ['tab', 'dialog'];

export function parse(hash: string): Route {
  const [path, query = ''] = hash.replace(/^#\/?/, '').split('?');
  const params = Object.fromEntries(new URLSearchParams(query));
  const [kind, a, b, c] = path.split('/').map(decodeURIComponent);
  const p = Object.keys(params).length ? { params } : {};
  if (kind === 'p' && a && b) return { project: a, screen: b, record: c ? Number(c) : undefined, ...p };
  if (kind === 'deck' && a) return { screen: a, record: b ? Number(b) : undefined, ...p };
  return { screen: 'cockpit', ...p };
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

// screenKey names the screen as the server draws it: a change of a tab or a dialog does not draw it again.
export const screenKey = (r: Route) => href({ ...r, params: serverParams(r) });

// The trail holds the addresses visited in this tab, so the back button shows only where there is a way back.
let trail: string[] = [location.hash || '#/'];
const subs = new Set<() => void>();
let route = parse(location.hash);
window.addEventListener('hashchange', () => {
  const now = location.hash || '#/';
  if (trail.length > 1 && trail[trail.length - 2] === now) trail = trail.slice(0, -1);
  else if (trail[trail.length - 1] !== now) trail = [...trail, now];
  route = parse(now);
  subs.forEach((f) => f());
});
const subscribe = (f: () => void) => (subs.add(f), () => subs.delete(f));

export const useRoute = () => useSyncExternalStore(subscribe, () => route);
const depth = () => trail.length;
export const useCanGoBack = () => useSyncExternalStore(subscribe, depth) > 1;

export function go(r: Route) {
  location.hash = href(r);
}

export function back() {
  history.back();
}

const withParam = (name: string, value: string | null): Route => {
  const params = { ...(route.params ?? {}) };
  if (value === null || value === '') delete params[name];
  else params[name] = value;
  return { ...route, params: Object.keys(params).length ? params : undefined };
};

// setParam changes one parameter of the current address in a new step of the history; null removes it.
export function setParam(name: string, value: string | null) {
  go(withParam(name, value));
}

// replaceParam changes one parameter of the current address in place, without a step of the history.
export function replaceParam(name: string, value: string | null) {
  const to = href(withParam(name, value));
  if (to === (location.hash || '#/')) return;
  trail = [...trail.slice(0, -1), to];
  location.replace(to);
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
