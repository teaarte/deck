import i18n from './i18n';
const params = new URLSearchParams(location.search);
const fromUrl = params.get('token');
if (fromUrl) {
  localStorage.setItem('deck-token', fromUrl);
  params.delete('token');
  history.replaceState(null, '', location.pathname + (params.size ? '?' + params : '') + location.hash);
}

export const token = (): string => localStorage.getItem('deck-token') ?? '';
export const setToken = (t: string) => localStorage.setItem('deck-token', t);

export class CommandError extends Error {}

// authed is every request of the page to deck, with the owner's token; deck's own refusal of the token signs the page
// out, while a model's refused key is a 401 without the challenge.
export async function authed(url: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  headers.set('Authorization', 'Bearer ' + token());
  const r = await fetch(url, { ...init, headers });
  if (r.status === 401 && r.headers.has('WWW-Authenticate')) {
    localStorage.removeItem('deck-token');
    location.reload();
  }
  return r;
}

// call invokes a registry command and returns its JSON result; a refusal throws the server's text in the language of
// the interface. A small call outlives the page, so a press made as the page leaves still goes.
export async function call<T = unknown>(command: string, input: Record<string, unknown> = {}): Promise<T> {
  const body = JSON.stringify(input);
  const r = await authed('/api/commands/' + encodeURIComponent(command), {
    method: 'POST', headers: { 'Content-Type': 'application/json', 'Deck-Lang': i18n.language ?? '' }, body, keepalive: body.length < 60000,
  });
  const text = await r.text();
  if (!r.ok) throw new CommandError(text.trim() || r.statusText);
  return (text ? JSON.parse(text) : null) as T;
}

export interface Uploaded {
  id: number;
  name: string;
}

// upload sends files to a project; commands then take them by id.
export async function upload(project: string, files: File[]): Promise<Uploaded[]> {
  const body = new FormData();
  files.forEach((f) => body.append('file', f, f.name));
  const r = await authed('/api/files?project=' + encodeURIComponent(project), { method: 'POST', body });
  const text = await r.text();
  if (!r.ok) throw new CommandError(text.trim() || r.statusText);
  return JSON.parse(text) as Uploaded[];
}

// saveBlob hands a file to the browser to keep under its name.
export function saveBlob(blob: Blob, name: string) {
  const a = document.createElement('a');
  a.href = URL.createObjectURL(new Blob([blob], { type: 'application/octet-stream' }));
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 60000);
}

// fetchFile reads a file deck gives at its address, with the type it is stored with.
export async function fetchFile(url: string): Promise<Blob> {
  const r = await authed(url);
  if (!r.ok) throw new CommandError((await r.text()).trim() || r.statusText);
  return new Blob([await r.arrayBuffer()], { type: r.headers.get('Content-Type') ?? 'application/octet-stream' });
}

