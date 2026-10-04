import { call } from './lib/api';

// keyBytes turns the base64url VAPID key of deck into the bytes PushManager takes.
function keyBytes(key: string): Uint8Array<ArrayBuffer> {
  const b64 = (key + '='.repeat((4 - (key.length % 4)) % 4)).replace(/-/g, '+').replace(/_/g, '/');
  const raw = atob(b64);
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

const b64url = (buf: ArrayBuffer | null) =>
  buf ? btoa(String.fromCharCode(...new Uint8Array(buf))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '') : '';

export const pushSupported = () => 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window;

// currentPush is the subscription of this device, if it has one.
export async function currentPush(): Promise<PushSubscription | null> {
  if (!pushSupported()) return null;
  const reg = await navigator.serviceWorker.ready;
  return reg.pushManager.getSubscription();
}

// subscribePush asks the browser for pushes of deck and hands the subscription to deck.
export async function subscribePush(device: string): Promise<void> {
  if ((await Notification.requestPermission()) !== 'granted') throw new Error('notifications are not allowed for deck');
  const { key } = await call<{ key: string }>('deck.push_key');
  const reg = await navigator.serviceWorker.ready;
  const old = await reg.pushManager.getSubscription();
  if (old) await old.unsubscribe();
  const sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(key) });
  try {
    await register(sub, device);
  } catch (e) {
    await sub.unsubscribe();
    throw e;
  }
}

const register = (sub: PushSubscription, device: string) =>
  call('deck.push_subscribe', { endpoint: sub.endpoint, p256dh: b64url(sub.getKey('p256dh')), auth: b64url(sub.getKey('auth')), device });

// reconcilePush tells deck again of the subscription this browser holds, so a device deck dropped comes back.
export async function reconcilePush(device: string): Promise<boolean> {
  const sub = await currentPush();
  if (!sub) return false;
  await register(sub, device);
  return true;
}

// unsubscribePush stops pushes to this device, in the browser and in deck.
export async function unsubscribePush(): Promise<void> {
  const sub = await currentPush();
  if (!sub) return;
  await call('deck.push_unsubscribe', { endpoint: sub.endpoint }).catch(() => undefined);
  await sub.unsubscribe();
}
