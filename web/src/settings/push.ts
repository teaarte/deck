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
async function currentPush(): Promise<PushSubscription | null> {
  if (!pushSupported()) return null;
  const reg = await navigator.serviceWorker.ready;
  return reg.pushManager.getSubscription();
}

// Subscription is what deck keeps of the subscription of this device.
export interface Subscription { endpoint: string; p256dh?: string; auth?: string }

const of = (sub: PushSubscription): Subscription => ({ endpoint: sub.endpoint, p256dh: b64url(sub.getKey('p256dh')), auth: b64url(sub.getKey('auth')) });

// subscribePush asks the browser for pushes of deck by its key and hands the subscription to deck by tell.
export async function subscribePush(key: string, tell: (s: Subscription) => Promise<void>): Promise<void> {
  if ((await Notification.requestPermission()) !== 'granted') throw new Error('notifications are not allowed for deck');
  const reg = await navigator.serviceWorker.ready;
  const old = await reg.pushManager.getSubscription();
  if (old) await old.unsubscribe();
  const sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(key) });
  try {
    await tell(of(sub));
  } catch (e) {
    await sub.unsubscribe();
    throw e;
  }
}

// reconcilePush tells deck again of the subscription this browser holds, so a device deck dropped comes back.
export async function reconcilePush(tell: (s: Subscription) => Promise<void>): Promise<boolean> {
  const sub = await currentPush();
  if (!sub) return false;
  await tell(of(sub));
  return true;
}

// unsubscribePush stops pushes to this device, in the browser and in deck.
export async function unsubscribePush(tell: (s: Subscription) => Promise<void>): Promise<void> {
  const sub = await currentPush();
  if (!sub) return;
  await tell({ endpoint: sub.endpoint }).catch(() => undefined);
  await sub.unsubscribe();
}
