// Files shared to deck wait in the cache the service worker put them in until a form sends them.
const CACHE = 'deck-share';
let shared: Promise<File[]> | undefined;

async function read(): Promise<File[]> {
  if (!('caches' in window)) return [];
  const cache = await caches.open(CACHE);
  const out: File[] = [];
  for (const req of await cache.keys()) {
    const r = await cache.match(req);
    if (r) {
      const name = decodeURIComponent(r.headers.get('x-name') ?? 'shared');
      out.push(new File([await r.blob()], name, { type: r.headers.get('content-type') ?? '' }));
    }
  }
  return out;
}

export const sharedFiles = (): Promise<File[]> => (shared ??= read());

// sharedSent drops the shared files once a command took them.
export async function sharedSent() {
  shared = Promise.resolve([]);
  if ('caches' in window) await caches.delete(CACHE);
}
