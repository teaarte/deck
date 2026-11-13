import { startAuthentication } from '@simplewebauthn/browser';
import i18n from './i18n';
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

export interface Begun { ceremony: string; options: { publicKey: never } }

// rite is a step of signing in; deck's refusal throws its word.
export async function rite(step: string, body: object): Promise<Begun> {
  const r = await fetch('/auth/' + step, {
    method: 'POST', headers: { 'Content-Type': 'application/json', 'Deck-Lang': i18n.language ?? '' }, body: JSON.stringify(body),
  });
  const text = await r.text();
  if (!r.ok) throw new Error(text.trim() || r.statusText);
  return JSON.parse(text) as Begun;
}

// login signs this browser in by a passkey it holds: a new session, whose start is a fresh touch of the passkey.
export async function login() {
  const b = await rite('login/begin', {});
  const credential = await startAuthentication({ optionsJSON: b.options.publicKey });
  await rite('login/finish', { ceremony: b.ceremony, credential });
}

// Touch is a call waiting for the owner to touch their passkey: deck's word on why, and the ends of the wait.
export interface Touch { why: string; done: () => void; drop: () => void }

const touch = createStore<Touch | null>(null);
export const useTouch = () => useStore(touch);
let waiting: Promise<void> | undefined;

// touched waits until the owner touched their passkey for a call that takes a fresh one, once for every call that asks
// meanwhile; it throws deck's word when they do not.
export function touched(why: string): Promise<void> {
  waiting ??= new Promise<void>((resolve, reject) => {
    const end = (ok: boolean) => {
      touch.set(null);
      waiting = undefined;
      if (ok) resolve();
      else reject(new Error(why));
    };
    touch.set({ why, done: () => end(true), drop: () => end(false) });
  });
  return waiting;
}
