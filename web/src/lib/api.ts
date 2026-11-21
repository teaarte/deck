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

// Effect is what a call did, as deck says it in Deck-Effect: done, started, waits for the owner, or changed nothing.
export type Effect = 'done' | 'started' | 'waiting' | 'unchanged';

// Answer is the JSON result of a command and, for a read, the topics after which it gives another; what the call did.
export interface Answer<T> { data: T; topics: string[]; effect: Effect }

// Refusal is a call deck refused or failed, with its HTTP status: 410 says what it acted on is over already.
export class Refusal extends Error {
  constructor(text: string, readonly status: number) { super(text); }
}

// newKey is the key of a press; it works on plain http, where randomUUID does not.
export function newKey(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) => b.toString(16).padStart(2, '0')).join('');
}

// Kept is the key of a press until it gets an answer, for a press again with the same input: another input is another
// call, with a key of its own.
export class Kept {
  private key = '';
  private body = '';
  for(input: unknown): string {
    const body = JSON.stringify(input ?? null);
    if (!this.key || body !== this.body) [this.key, this.body] = [newKey(), body];
    return this.key;
  }
  answered(lost?: boolean) { if (!lost) this.key = ''; }
  forget() { this.key = ''; }
}

// invoke calls a registry command, with the key of its press; a refusal throws the server's text. A small call outlives
// the page; one that takes a fresh passkey waits for the owner's touch, then goes again.
export async function invoke<T = unknown>(command: string, input: Record<string, unknown> = {}, key?: string): Promise<Answer<T>> {
  const body = JSON.stringify(input);
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (key) headers['Deck-Key'] = key;
  const send = () => authed('/api/commands/' + encodeURIComponent(command), {
    method: 'POST', headers, body, keepalive: body.length < 60000,
  });
  let r = await send();
  if (r.status === 428) {
    await touched((await r.text()).trim());
    r = await send();
  }
  const text = await r.text();
  if (!r.ok) throw new Refusal(text.trim() || r.statusText, r.status);
  const topics = r.headers.get('Deck-Topics');
  const effect = (r.headers.get('Deck-Effect') || 'done') as Effect;
  return { data: (text ? JSON.parse(text) : null) as T, topics: topics ? topics.split(',') : [], effect };
}

// call is the result of a command alone.
export const call = async <T = unknown>(command: string, input: Record<string, unknown> = {}, key?: string): Promise<T> =>
  (await invoke<T>(command, input, key)).data;

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

