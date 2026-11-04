import { createStore, useStore } from './store';

// code is that of a link that pairs this browser, read once off #pair=…, which no server sees; the route after it
// stays. Signing in spends it.
let code = (() => {
  const m = /^#pair=([A-Za-z0-9_-]+)(.*)$/.exec(location.hash);
  if (!m) return '';
  history.replaceState(null, '', location.pathname + location.search + (m[2] ? '#' + m[2] : ''));
  return m[1];
})();
export const pairCode = () => code;

// signedOut is deck's word on why the page is not signed in, empty while it is.
const out = createStore(code ? ' ' : '');
export const useSignedOut = () => useStore(out);
export const signedOut = () => out.get();

// signedIn shows deck again once the browser signed in.
export const signedIn = () => {
  code = '';
  out.set('');
};

// signOut shows the sign-in with deck's word.
export function signOut(why: string) {
  out.set(why || ' ');
}
