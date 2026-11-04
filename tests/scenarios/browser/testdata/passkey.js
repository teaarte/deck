// A passkey authenticator in the page, for WebKit, which Playwright gives no virtual one: it makes ES256 passkeys
// with attestation none, keeps them in localStorage and signs assertions as a platform authenticator does.
(() => {
  if (window.top !== window || !window.isSecureContext || typeof PublicKeyCredential !== 'function') return;
  const KEY = 'deck-test-passkeys';
  const enc = new TextEncoder();
  const b64u = (b) => btoa(String.fromCharCode(...b)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  const unb64u = (s) => Uint8Array.from(atob(s.replace(/-/g, '+').replace(/_/g, '/')), (c) => c.charCodeAt(0));
  const u8 = (x) => (x instanceof ArrayBuffer ? new Uint8Array(x) : new Uint8Array(x.buffer, x.byteOffset, x.byteLength));
  const cat = (...parts) => {
    const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
    let i = 0;
    for (const p of parts) { out.set(p, i); i += p.length; }
    return out;
  };
  const head = (major, n) => (n < 24 ? Uint8Array.of((major << 5) | n) : n < 256 ? Uint8Array.of((major << 5) | 24, n) : Uint8Array.of((major << 5) | 25, n >> 8, n & 255));
  const cbor = (v) => {
    if (typeof v === 'number') return v >= 0 ? head(0, v) : head(1, -1 - v);
    if (typeof v === 'string') { const b = enc.encode(v); return cat(head(3, b.length), b); }
    if (v instanceof Uint8Array) return cat(head(2, v.length), v);
    return cat(head(5, v.size), ...[...v].flatMap(([k, x]) => [cbor(k), cbor(x)]));
  };
  const sha = async (b) => new Uint8Array(await crypto.subtle.digest('SHA-256', b));
  const der = (sig) => {
    const int = (x) => {
      let i = 0;
      while (i < x.length - 1 && x[i] === 0) i++;
      x = x.slice(i);
      if (x[0] & 0x80) x = cat(Uint8Array.of(0), x);
      return cat(Uint8Array.of(2, x.length), x);
    };
    const r = int(sig.slice(0, 32)), s = int(sig.slice(32));
    return cat(Uint8Array.of(0x30, r.length + s.length), r, s);
  };
  const saved = () => JSON.parse(localStorage.getItem(KEY) || '[]');
  const client = (type, challenge) => enc.encode(JSON.stringify({ type, challenge: b64u(u8(challenge)), origin: location.origin, crossOrigin: false }));
  const credential = (id, response) => ({
    id: b64u(id), rawId: id.buffer, type: 'public-key', authenticatorAttachment: 'platform', response,
    getClientExtensionResults: () => ({}),
  });
  // On the prototype: WebKit keeps no property set on navigator.credentials.
  const put = (name, fn) => Object.defineProperty(CredentialsContainer.prototype, name, { value: fn, configurable: true, writable: true });
  put('create', async ({ publicKey: o }) => {
    const rp = o.rp.id ?? location.hostname;
    const pair = await crypto.subtle.generateKey({ name: 'ECDSA', namedCurve: 'P-256' }, true, ['sign', 'verify']);
    const raw = new Uint8Array(await crypto.subtle.exportKey('raw', pair.publicKey));
    const id = crypto.getRandomValues(new Uint8Array(16));
    const cose = cbor(new Map([[1, 2], [3, -7], [-1, 1], [-2, raw.slice(1, 33)], [-3, raw.slice(33)]]));
    const data = cat(await sha(enc.encode(rp)), Uint8Array.of(0x45), new Uint8Array(4), new Uint8Array(16), Uint8Array.of(0, id.length), id, cose);
    const att = cbor(new Map([['fmt', 'none'], ['attStmt', new Map()], ['authData', data]]));
    const key = await crypto.subtle.exportKey('jwk', pair.privateKey);
    localStorage.setItem(KEY, JSON.stringify([...saved(), { id: b64u(id), rp, user: b64u(u8(o.user.id)), key, n: 0 }]));
    return credential(id, { clientDataJSON: client('webauthn.create', o.challenge).buffer, attestationObject: att.buffer, getTransports: () => ['internal'] });
  });
  put('get', async ({ publicKey: o }) => {
    const rp = o.rpId ?? location.hostname;
    const all = saved();
    const c = all.find((x) => x.rp === rp);
    if (!c) throw new DOMException('no passkey here', 'NotAllowedError');
    c.n++;
    localStorage.setItem(KEY, JSON.stringify(all));
    const data = cat(await sha(enc.encode(rp)), Uint8Array.of(0x05), Uint8Array.of(c.n >>> 24, (c.n >>> 16) & 255, (c.n >>> 8) & 255, c.n & 255));
    const cd = client('webauthn.get', o.challenge);
    const key = await crypto.subtle.importKey('jwk', c.key, { name: 'ECDSA', namedCurve: 'P-256' }, false, ['sign']);
    const sig = der(new Uint8Array(await crypto.subtle.sign({ name: 'ECDSA', hash: 'SHA-256' }, key, cat(data, await sha(cd)))));
    return credential(unb64u(c.id), { clientDataJSON: cd.buffer, authenticatorData: data.buffer, signature: sig.buffer, userHandle: unb64u(c.user).buffer });
  });
})();
