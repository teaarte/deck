import { createStore, useStore } from './store';

// pairCode is the code of a link that pairs this browser, read once off #pair=…, which no server sees; the route
// after it stays.
export const pairCode = (() => {
  const m = /^#pair=([A-Za-z0-9_-]+)(.*)$/.exec(location.hash);
  if (!m) return '';
  history.replaceState(null, '', location.pathname + location.search + (m[2] ? '#' + m[2] : ''));
  return m[1];
})();

// signedOut is deck's word on why the page is not signed in, empty while it is.
const out = createStore(pairCode ? ' ' : '');
export const useSignedOut = () => useStore(out);
export const signedOut = () => out.get();

// signedIn shows deck again once the browser signed in.
export const signedIn = () => out.set('');

// signOut shows the sign-in with deck's word.
export function signOut(why: string) {
  out.set(why || ' ');
}
