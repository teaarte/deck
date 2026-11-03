import { createStore, useStore } from './store';

// KEY is where the page keeps the key it signs in with, given once in the address: the launcher's window and deck's
// own browser send it in the header; a browser signs in by its cookie and keeps none.
const KEY = 'deck-key';

// taken reads #key=… or #pair=… off the address, which no server sees, and leaves the route after it.
function taken(name: string): string {
  const m = new RegExp('^#' + name + '=([A-Za-z0-9_-]+)(.*)$').exec(location.hash);
  if (!m) return '';
  history.replaceState(null, '', location.pathname + location.search + (m[2] ? '#' + m[2] : ''));
  return m[1];
}

const given = taken('key');
if (given) sessionStorage.setItem(KEY, given);

// key is the key of the page, empty when it signs in by a cookie.
export const key = (): string => sessionStorage.getItem(KEY) ?? '';

// pairCode is the code of a link that pairs this browser, read once.
export const pairCode = taken('pair');

// signedOut is deck's word on why the page is not signed in, empty while it is.
const out = createStore(pairCode ? ' ' : '');
export const useSignedOut = () => useStore(out);
export const signedOut = () => out.get();

// keyed says the page signed in with a key deck refused since, as the launcher's window does once it is revoked.
let keyed = false;
export const wasKeyed = () => keyed;

// signedIn shows deck again once the browser signed in.
export const signedIn = () => out.set('');

// signOut shows the sign-in with deck's word; a key deck refused is dropped.
export function signOut(why: string) {
  if (key()) keyed = true;
  sessionStorage.removeItem(KEY);
  out.set(why || ' ');
}
