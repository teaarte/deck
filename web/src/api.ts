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

// call invokes a registry command and returns its JSON result; a refusal throws the server's text.
export async function call<T = unknown>(command: string, input: Record<string, unknown> = {}): Promise<T> {
  const r = await fetch('/api/commands/' + encodeURIComponent(command), {
    method: 'POST',
    headers: { Authorization: 'Bearer ' + token(), 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  const text = await r.text();
  if (r.status === 401) {
    localStorage.removeItem('deck-token');
    location.reload();
  }
  if (!r.ok) throw new CommandError(text.trim() || r.statusText);
  return (text ? JSON.parse(text) : null) as T;
}

type Listener = { topic: (topic: string) => void; state: (live: boolean) => void };
const listeners = new Set<Listener>();
let source: EventSource | undefined;
let retry: ReturnType<typeof setTimeout> | undefined;
let live = false;

// openSource keeps one live channel for every screen of the page and opens it again when the browser gives up.
function openSource() {
  source = new EventSource('/api/events?token=' + encodeURIComponent(token()));
  source.onopen = () => { live = true; listeners.forEach((l) => l.state(true)); };
  source.onmessage = (e) => listeners.forEach((l) => l.topic(String(e.data)));
  source.onerror = () => {
    live = false;
    listeners.forEach((l) => l.state(false));
    if (source?.readyState === EventSource.CLOSED && listeners.size) retry = setTimeout(openSource, 3000);
  };
}

// events follows the live channel of the page; the last one to leave closes it.
export function events(onTopic: (topic: string) => void, onState: (live: boolean) => void): () => void {
  const l: Listener = { topic: onTopic, state: onState };
  listeners.add(l);
  if (!source || source.readyState === EventSource.CLOSED) openSource();
  else if (live) onState(true);
  return () => {
    listeners.delete(l);
    if (listeners.size) return;
    clearTimeout(retry);
    source?.close();
    source = undefined;
    live = false;
  };
}

export interface Uploaded {
  id: number;
  name: string;
}

// upload sends files to a project; commands then take them by id.
export async function upload(project: string, files: File[]): Promise<Uploaded[]> {
  const body = new FormData();
  files.forEach((f) => body.append('file', f, f.name));
  const r = await fetch('/api/files?project=' + encodeURIComponent(project), {
    method: 'POST', headers: { Authorization: 'Bearer ' + token() }, body,
  });
  const text = await r.text();
  if (!r.ok) throw new CommandError(text.trim() || r.statusText);
  return JSON.parse(text) as Uploaded[];
}

// download saves a stored file through the browser; the token goes in the header, not in the address.
export async function download(id: number): Promise<void> {
  const r = await fetch('/api/files/' + id, { headers: { Authorization: 'Bearer ' + token() } });
  if (!r.ok) throw new CommandError((await r.text()).trim() || r.statusText);
  saveBlob(new Blob([await r.arrayBuffer()]), fileName(r.headers.get('Content-Disposition') ?? ''));
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
  const r = await fetch(url, { headers: { Authorization: 'Bearer ' + token() } });
  if (!r.ok) throw new CommandError((await r.text()).trim() || r.statusText);
  return new Blob([await r.arrayBuffer()], { type: r.headers.get('Content-Type') ?? 'application/octet-stream' });
}

// fileName reads the name of Content-Disposition: percent-encoded in filename*, quoted or bare in filename.
function fileName(disposition: string): string {
  const encoded = /filename\*=utf-8''([^;]+)/i.exec(disposition);
  if (encoded) return decodeURIComponent(encoded[1]);
  const quoted = /filename="((?:[^"\\]|\\.)*)"/i.exec(disposition);
  if (quoted) return quoted[1].replace(/\\(.)/g, '$1');
  return /filename=([^;]+)/i.exec(disposition)?.[1].trim() ?? 'file';
}
