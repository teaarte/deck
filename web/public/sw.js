// deck works only online; the worker is here for installing and for files shared to it.
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()));
self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url);
  if (e.request.method !== 'POST' || url.pathname !== '/share') return;
  e.respondWith((async () => {
    const form = await e.request.formData();
    await caches.delete('deck-share');
    const cache = await caches.open('deck-share');
    const files = form.getAll('file').filter((f) => typeof f !== 'string');
    const text = ['title', 'text', 'url'].map((k) => form.get(k)).filter((v) => typeof v === 'string' && v).join('\n');
    if (text) files.push(new File([text], 'shared.txt', { type: 'text/plain' }));
    await Promise.all(files.map((f, i) => cache.put('/shared/' + i,
      new Response(f, { headers: { 'content-type': f.type, 'x-name': encodeURIComponent(f.name) } }))));
    return Response.redirect('/#/deck/share', 303);
  })());
});

// A push shows a notification; a tap opens deck where the push points.
self.addEventListener('push', (e) => {
  let m = { title: 'deck', body: '', url: '/' };
  try { m = { ...m, ...e.data.json() }; } catch { /* a push without JSON shows as deck */ }
  e.waitUntil(self.registration.showNotification(m.title, { body: m.body, data: { url: m.url }, icon: '/icon-192.png' }));
});
self.addEventListener('notificationclick', (e) => {
  e.notification.close();
  const url = new URL(e.notification.data?.url || '/', self.location.origin).href;
  e.waitUntil((async () => {
    const tabs = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
    const tab = tabs.find((c) => new URL(c.url).origin === self.location.origin);
    if (tab) {
      await tab.focus();
      return tab.navigate(url).catch(() => self.clients.openWindow(url));
    }
    return self.clients.openWindow(url);
  })());
});
