import i18n from './i18n';
import { signOut, touched } from './signin';

// authed is every request of the page to deck, by the browser's cookie; deck's own refusal signs the page out, while a
// model's refused key is a 401 without the challenge.
export async function authed(url: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  headers.set('Deck-Lang', i18n.language ?? '');
  const r = await fetch(url, { ...init, headers });
  if (r.status === 401 && r.headers.has('WWW-Authenticate')) signOut((await r.clone().text()).trim());
  return r;
}

// Answer is the JSON result of a command and, for a read, the topics after which it gives another.
export interface Answer<T> { data: T; topics: string[] }

// invoke calls a registry command; a refusal throws the server's text in the language of the interface. A small call
// outlives the page; one that takes a fresh passkey waits for the owner's touch, then goes again.
export async function invoke<T = unknown>(command: string, input: Record<string, unknown> = {}): Promise<Answer<T>> {
  const body = JSON.stringify(input);
  const send = () => authed('/api/commands/' + encodeURIComponent(command), {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body, keepalive: body.length < 60000,
  });
  let r = await send();
  if (r.status === 428) {
    await touched((await r.text()).trim());
    r = await send();
  }
  const text = await r.text();
  if (!r.ok) throw new Error(text.trim() || r.statusText);
  const topics = r.headers.get('Deck-Topics');
  return { data: (text ? JSON.parse(text) : null) as T, topics: topics ? topics.split(',') : [] };
}

// call is the result of a command alone.
export const call = async <T = unknown>(command: string, input: Record<string, unknown> = {}): Promise<T> => (await invoke<T>(command, input)).data;

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
  if (!r.ok) throw new Error(text.trim() || r.statusText);
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
  if (!r.ok) throw new Error((await r.text()).trim() || r.statusText);
  return new Blob([await r.arrayBuffer()], { type: r.headers.get('Content-Type') ?? 'application/octet-stream' });
}

